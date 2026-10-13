// Signaling and session glue for the 1:1 voice call.
//
// This module owns everything with a lifecycle — the RTCPeerConnection
// (wrapped by rtc/peer.ts CallPeer), the local microphone, the hidden audio
// element playing the remote stream, and the pending offer (plus the caller's
// ICE candidates that trickle in during the ring) stored while the phone
// "rings" — and drives the zustand store (store/call.ts) so CallView
// can render it. It never touches React.
//
// Wire protocol (see internal/ws/handler.go):
//   - call_offer / call_answer / call_ice are TARGETED: the client sends
//     target_user_id and a JSON-string content (SDP or ICE candidate); the
//     server delivers content verbatim, stamping the sender identity from
//     the JWT. If the target is offline, offer/answer earn a client-local
//     error frame "user is not online".
//   - call_join / call_leave are room broadcasts with no payload (the
//     sender receives its own event too).
//   - The server broadcasts to everyone; room filtering is client-side.
//
// WS loss during an active call: the peer connection is NOT torn down. The
// media path is pure P2P (ICE + DTLS + SRTP); the WebSocket only carries
// the signaling that set it up. The socket-store singleton reconnects on
// its own, and late signaling is harmless — CallPeer queues ICE candidates
// until a remote description exists and drops stale ones after hang-up.
// The only real casualty of a signaling blackout is a brand-new offer we
// can't receive; an in-flight call keeps talking. Sessions that can no
// longer progress (no answer, no ICE, lost counterpart) self-destruct on
// the ring/answer/connect deadlines below, so a dead call never holds the
// microphone forever.

import { getToken, useAuth } from '@/store/auth'
import { useCall, type CallPeerInfo } from '@/store/call'
import { showNotification } from '@/lib/notifications'
import { apiClient } from '@/api/client'
import { CallPeer } from '@/rtc/peer'
import { openMic, stopStream } from '@/rtc/media'
import { getSocket, subscribe } from '@/ws/socket-store'
import type { ServerMessage } from '@/ws/socket'

// --- module state ------------------------------------------------------------

// Dead-session deadlines. The server reports an offline target only for
// offer/answer; once the counterpart's tab dies mid-ring or mid-connect there
// is no event at all (a severed WS broadcasts nothing), so each phase arms
// its own deadline and a stuck session tears itself down — releasing the
// microphone — instead of ringing/connecting forever.
const RING_TIMEOUT_MS = 60_000 // 'incoming': nobody pressed Accept
const ANSWER_TIMEOUT_MS = 60_000 // 'outgoing': the callee never answered
const CONNECT_TIMEOUT_MS = 60_000 // post-answer: ICE never converged

/** The live peer connection; null outside a call session. */
let peer: CallPeer | null = null
/** The local microphone stream; stopped on teardown to release the device. */
let micStream: MediaStream | null = null
/** The offer that started the ring; applied once answerCall builds the peer. */
let pendingOffer: string | null = null
/**
 * Remote ICE candidates the caller kept trickle-ICE-ing while the phone was
 * still ringing — i.e. before answerCall built the peer. Dropped on the
 * floor they would leave the answered call without a single remote candidate
 * (STUN-only ICE then never converges); they are handed to CallPeer on
 * accept, which re-queues them until the remote description is applied.
 */
let pendingCandidates: RTCIceCandidateInit[] = []
/** Hidden <audio> element the remote stream plays through. */
let remoteAudio: HTMLAudioElement | null = null
/** Currently armed dead-session deadline; null while 'active'/idle. */
let callTimeout: ReturnType<typeof setTimeout> | null = null
/** initCallSignaling guard — the subscription lives for the app's lifetime. */
let initialized = false

// --- helpers ------------------------------------------------------------------

/**
 * Our own user id, decoded from the JWT payload (claim `sub`, with the
 * custom `uid` claim as a fallback — both are set by auth.IssueToken and
 * carry the same uuid).
 *
 * Decoding without verifying the signature is fine here: the value only
 * picks the perfect-negotiation role and filters our own broadcasts, never
 * authorizes anything — the server derives identity from the signed token.
 * Returns '' when undecodable; '' sorts before every uuid, so we end up the
 * polite side, deterministically.
 */
function currentUserId(): string {
  const token = getToken()
  if (!token) return ''
  const part = token.split('.')[1]
  if (!part) return ''
  try {
    const b64 = part.replace(/-/g, '+').replace(/_/g, '/')
    const padded = b64 + '='.repeat((4 - (b64.length % 4)) % 4)
    const bytes = Uint8Array.from(atob(padded), (c) => c.charCodeAt(0))
    const claims = JSON.parse(new TextDecoder().decode(bytes)) as {
      sub?: unknown
      uid?: unknown
    }
    if (typeof claims.sub === 'string' && claims.sub) return claims.sub
    if (typeof claims.uid === 'string' && claims.uid) return claims.uid
    return ''
  } catch {
    return ''
  }
}

/** Fetch STUN/TURN servers for the call (short-lived TURN credentials —
 * the endpoint is fetched fresh per call, never cached). */
async function fetchIceServers(): Promise<RTCIceServer[]> {
  const { data, error } = await apiClient.GET('/api/v1/call/config', {})
  if (error || !data) throw new Error('Failed to load call configuration')
  // The generated schema marks every field optional (the spec declares no
  // `required`); the server always sends urls, credentials only for TURN.
  return (data.ice_servers ?? []).map((s) => ({
    urls: s.urls ?? [],
    username: s.username,
    credential: s.credential,
  }))
}

/**
 * True while the store still describes the in-flight session for this
 * room+peer. Async gaps in start/answer (config fetch, getUserMedia prompt)
 * race against the user hanging up or a second call replacing the first —
 * whatever we hold by then must be released, not wired into the new call.
 */
function isCurrentSession(roomId: string, userId: string): boolean {
  const s = useCall.getState()
  return (
    s.roomId === roomId &&
    s.peer?.userId === userId &&
    (s.status === 'outgoing' || s.status === 'connecting' || s.status === 'active')
  )
}

/** Play the peer's audio through a hidden <audio> element. */
function attachRemoteStream(stream: MediaStream): void {
  remoteAudio ??= document.createElement('audio')
  remoteAudio.autoplay = true
  remoteAudio.hidden = true
  if (!remoteAudio.parentNode) document.body.appendChild(remoteAudio)
  remoteAudio.srcObject = stream
  void remoteAudio.play().catch(() => {
    // Autoplay can still be refused; the stream stays attached, so unmuting
    // the tab resumes playback. Best-effort, like showNotification.
  })
}

/** Release every session resource: peer, microphone, ring offer, audio. */
function teardown(): void {
  peer?.close() // detaches all pc handlers — no stale callbacks can fire
  peer = null
  if (micStream) {
    stopStream(micStream)
    micStream = null
  }
  pendingOffer = null
  pendingCandidates = []
  clearCallTimeout()
  if (remoteAudio) {
    remoteAudio.pause()
    remoteAudio.srcObject = null
    remoteAudio.remove()
    remoteAudio = null
  }
}

/** Tear down and land in 'ended' with an error message. Callers must have
 * checked isCurrentSession — teardown must never hit a newer session. */
function failCall(message: string): void {
  teardown()
  useCall.getState().endCall(message)
}

// --- dead-session deadlines ---------------------------------------------------

function clearCallTimeout(): void {
  if (callTimeout) {
    clearTimeout(callTimeout)
    callTimeout = null
  }
}

/**
 * (Re)arm the deadline of the phase the session just entered. One timer per
 * tab: re-arming on every transition replaces the previous phase's deadline;
 * teardown/markActive clear it. `roomId`+`peerId` guard the callback against
 * firing into a session that has since been replaced.
 */
function armCallTimeout(
  phase: 'ring' | 'answer' | 'connect',
  roomId: string,
  peerId: string,
): void {
  clearCallTimeout()
  const ms =
    phase === 'ring' ? RING_TIMEOUT_MS : phase === 'answer' ? ANSWER_TIMEOUT_MS : CONNECT_TIMEOUT_MS
  callTimeout = setTimeout(() => {
    callTimeout = null
    const s = useCall.getState()
    if (s.roomId !== roomId || s.peer?.userId !== peerId) return // replaced meanwhile
    if (s.status === 'incoming') {
      // Missed call: drop the invitation quietly and tell the caller's tab
      // the line is dead (their call_leave branch cancels "Calling…").
      pendingOffer = null
      pendingCandidates = []
      getSocket()?.sendCallLeave(roomId)
      s.endCall('Missed call')
    } else if (s.status === 'outgoing' || s.status === 'connecting') {
      getSocket()?.sendCallLeave(roomId)
      failCall(phase === 'answer' ? 'No answer' : 'Connection timed out')
    }
  }, ms)
}

/**
 * Stop ringing because ANOTHER TAB of this user resolved the call (accepted
 * it — its call_join echoes back to every tab of ours — or declined/timed it
 * out via call_leave). Without this the still-ringing tab could Accept (a
 * second answer the caller drops, leaving this tab stuck in 'connecting'
 * with the mic open) or Decline (a room-wide call_leave that tears down the
 * live call the other tab just established).
 */
function dismissIncomingFromAnotherTab(): void {
  pendingOffer = null
  pendingCandidates = []
  clearCallTimeout()
  useCall.getState().reset()
}

function createPeer(
  roomId: string,
  target: CallPeerInfo,
  iceServers: RTCIceServer[],
): CallPeer {
  // Perfect-negotiation roles must be complementary: both sides compare the
  // same pair of user ids, and uuids never collide, so exactly one of
  // `myId < peerId` / `peerId < myId` holds on each side.
  const polite = currentUserId() < target.userId
  return new CallPeer({
    iceServers,
    polite,
    sendOffer: (sdp) => {
      // A false return means the socket is mid-reconnect and the offer is
      // gone — the callee never sees it and the call can never start. Fail
      // loudly instead of an eternal "Calling…" card.
      if (!getSocket()?.sendCallOffer(roomId, target.userId, sdp) && isCurrentSession(roomId, target.userId)) {
        failCall('Chat connection is down')
      }
    },
    sendAnswer: (sdp) => getSocket()?.sendCallAnswer(roomId, target.userId, sdp),
    sendCandidate: (c) => getSocket()?.sendCallIce(roomId, target.userId, JSON.stringify(c)),
    onRemoteStream: attachRemoteStream,
    onStateChange: (state) => {
      // Belongs to a session that has since been replaced? Its handlers are
      // detached on close(), so this can only race the store write itself.
      if (!isCurrentSession(roomId, target.userId)) return
      if (state === 'connected') {
        // Media flows — no phase deadline applies anymore ('disconnected'
        // flaps may recover on their own).
        clearCallTimeout()
        useCall.getState().markActive()
      } else if (state === 'failed') {
        // ICE gave up entirely ('disconnected' may still recover, so it is
        // deliberately not terminal). The socket can be perfectly healthy —
        // only the call ends.
        failCall('Connection failed')
      }
    },
    onError: () => {
      // Soft channel: malformed/stale signaling inside CallPeer. Swallowed
      // on purpose — it never means the call is down.
    },
  })
}

// --- incoming frames ----------------------------------------------------------

/** Parse a JSON-string ICE candidate payload (RTCIceCandidateInit). */
function parseCandidate(content: string): RTCIceCandidateInit | null {
  try {
    const parsed = JSON.parse(content) as Partial<RTCIceCandidateInit>
    if (typeof parsed.candidate !== 'string') return null
    return parsed as RTCIceCandidateInit
  } catch {
    return null
  }
}

function handleCallMessage(m: ServerMessage): void {
  const call = useCall.getState()

  switch (m.type) {
    case 'call_offer': {
      // Targeted frame: the server already routed it to us via
      // target_user_id; room_id just labels which room the call belongs to.
      if (m.user_id === currentUserId()) return // paranoia; server forbids self-calls

      if (peer && call.peer?.userId === m.user_id) {
        // An offer from the user we're already signaling with — glare or
        // renegotiation. Perfect negotiation inside CallPeer resolves it;
        // whatever side is impolite wins, the polite one rolls back.
        peer.handleOffer(m.content)
        return
      }
      if (call.status !== 'idle' && call.status !== 'ended') {
        // The line is busy with someone else; drop it — the caller's side
        // shows "user is not online" only if we're offline, otherwise their
        // outgoing card simply never connects and they cancel.
        return
      }
      // A fresh invitation: start ringing. The SDP is held until the user
      // accepts — opening the microphone before that would light the mic
      // indicator for a call that may be declined.
      pendingOffer = m.content
      pendingCandidates = []
      call.setIncoming(m.room_id, { userId: m.user_id, username: m.username })
      armCallTimeout('ring', m.room_id, m.user_id)
      showNotification(m.username, 'Incoming call')
      return
    }

    case 'call_answer': {
      if (!peer || call.peer?.userId !== m.user_id) return
      peer.handleAnswer(m.content)
      // The answer landed: we are connecting now, not waiting anymore —
      // swap the answer deadline for the ICE deadline (the store status
      // stays 'outgoing' until connectionState reaches 'connected').
      if (call.roomId) armCallTimeout('connect', call.roomId, m.user_id)
      return
    }

    case 'call_ice': {
      const candidate = parseCandidate(m.content)
      if (!candidate) return
      if (call.status === 'incoming' && call.peer?.userId === m.user_id) {
        // Still ringing: the peer doesn't exist yet. The caller gathers
        // candidates throughout the ring — park them next to the offer and
        // hand everything to CallPeer on accept.
        pendingCandidates.push(candidate)
        return
      }
      if (!peer || call.peer?.userId !== m.user_id) return
      // Trickle in both directions; CallPeer queues candidates that arrive
      // before the remote description exists.
      peer.handleCandidate(candidate)
      return
    }

    case 'call_join':
      // Room broadcast with no payload; our own join echoes back to us.
      // Multi-tab: that echo means another tab of this user just accepted
      // the call (answerCall sends the join), so this tab must stop ringing
      // — see dismissIncomingFromAnotherTab.
      if (m.user_id === currentUserId() && call.status === 'incoming' && call.roomId === m.room_id) {
        dismissIncomingFromAnotherTab()
      }
      // Others joining needs no pair-call action — it becomes interesting
      // for group calls later.
      return

    case 'call_leave': {
      // Another tab of THIS user declined the ring or its ring deadline
      // fired: m.user_id is us while call.peer is the caller, so the peer
      // match below would not fire — quiet this tab's ring explicitly.
      if (m.user_id === currentUserId() && call.status === 'incoming' && call.roomId === m.room_id) {
        dismissIncomingFromAnotherTab()
        return
      }
      // The counterpart hung up. call_leave is a room broadcast, so filter
      // by peer identity; anything else (another member's call ending) is
      // not ours to react to.
      if (call.peer?.userId !== m.user_id) return
      if (call.status === 'idle' || call.status === 'ended') return
      hangUp()
      return
    }

    case 'error': {
      // The server's reply to signaling whose target is offline. Error
      // frames are client-local and unspecific — match on text, and only
      // while the frame could be ours, so unrelated errors (message edits
      // etc.) never hijack the call UI.
      if (m.text !== 'user is not online') return
      if (call.status === 'outgoing') {
        // Our offer found the target's every connection gone.
        failCall('User is not online')
      } else if (call.status === 'connecting') {
        // We just answered into the void: the caller's tabs all died while
        // the phone rang. Without this branch the callee would sit in
        // 'connecting' holding the microphone forever.
        failCall('The caller went offline')
      }
      return
    }
  }
}

// --- public API ---------------------------------------------------------------

/**
 * Subscribe the call machinery to the app-level socket. Idempotent — call
 * from wherever the protected area mounts (ProtectedRoute today). The
 * subscription is never dropped: the socket singleton is recreated on
 * re-login and events keep flowing into the same handler.
 */
export function initCallSignaling(): void {
  if (initialized) return
  initialized = true
  subscribe(handleCallMessage)

  // Logout mid-call: the CallView unmounts with the protected area, but the
  // peer, the microphone and the audio element live here in module scope —
  // release them or the browser keeps the mic indicator lit with no UI left
  // to hang up. (Importing useAuth is safe: auth imports neither of us.)
  useAuth.subscribe((state, prev) => {
    if (state.token === prev.token) return
    if (!state.token) {
      teardown()
      useCall.getState().reset()
    }
  })
}

/** Start a call to `target` inside `roomId`. A repeated start while a call
 * is in flight is ignored — exactly one call at a time. */
export async function startCall(roomId: string, target: CallPeerInfo): Promise<void> {
  const call = useCall.getState()
  if (call.status !== 'idle' && call.status !== 'ended') return
  call.setOutgoing(roomId, target)
  armCallTimeout('answer', roomId, target.userId)

  try {
    // A call started while the socket is mid-reconnect would capture the
    // microphone only to have its offer silently dropped by send() — bail
    // out before touching media (the Call button is disabled in this state,
    // this covers the races it can't).
    if (!getSocket()?.isOpen()) {
      failCall('Chat connection is down')
      return
    }
    const iceServers = await fetchIceServers()
    if (!isCurrentSession(roomId, target.userId)) return
    const stream = await openMic()
    if (!isCurrentSession(roomId, target.userId)) {
      // Cancelled while the mic prompt was up.
      stopStream(stream)
      return
    }
    micStream = stream
    peer = createPeer(roomId, target, iceServers)
    // Announce to the room first, then attach the mic tracks — that fires
    // negotiationneeded, whose offer goes out as a targeted call_offer via
    // the peer callbacks above.
    getSocket()?.sendCallJoin(roomId)
    peer.addMicStream(micStream)
  } catch (err) {
    // getUserMedia failures arrive as user-facing messages from openMic();
    // config/network failures as the message below.
    if (!isCurrentSession(roomId, target.userId)) return
    failCall(err instanceof Error ? err.message : 'Could not start the call')
  }
}

/** Accept the ringing call: mic, peer, and the answer generated by applying
 * the stored offer (sendAnswer → socket.sendCallAnswer). */
export async function answerCall(): Promise<void> {
  const call = useCall.getState()
  if (call.status !== 'incoming' || !call.roomId || !call.peer) return
  const roomId = call.roomId
  const target = call.peer
  const offer = pendingOffer
  call.setConnecting()
  armCallTimeout('connect', roomId, target.userId)

  try {
    // Same socket gate as startCall: an answer that can't be sent leaves
    // the caller ringing and this tab stuck in 'connecting' with the mic.
    if (!getSocket()?.isOpen()) {
      failCall('Chat connection is down')
      return
    }
    const iceServers = await fetchIceServers()
    if (!isCurrentSession(roomId, target.userId)) return
    const stream = await openMic()
    if (!isCurrentSession(roomId, target.userId)) {
      stopStream(stream)
      return
    }
    micStream = stream
    peer = createPeer(roomId, target, iceServers)
    // The caller's candidates that trickled in while we were ringing —
    // CallPeer re-queues them until the stored offer sets the remote
    // description, so feeding them here can't race the answer.
    const earlyCandidates = pendingCandidates
    pendingCandidates = []
    for (const candidate of earlyCandidates) peer.handleCandidate(candidate)
    getSocket()?.sendCallJoin(roomId)
    peer.addMicStream(stream)
    if (offer === null) {
      // Cannot happen ('incoming' implies a stored offer) — but without it
      // no answer would ever be generated, so fail loudly rather than hang.
      failCall('The call invitation was lost')
      return
    }
    // Feeding the offer drives setRemoteDescription → createAnswer → the
    // sendAnswer callback → socket.sendCallAnswer to the caller.
    peer.handleOffer(offer)
  } catch (err) {
    if (!isCurrentSession(roomId, target.userId)) return
    failCall(err instanceof Error ? err.message : 'Could not answer the call')
  }
}

/** Decline the ringing call: tell the room we're out and drop the offer. */
export function declineCall(): void {
  const call = useCall.getState()
  if (call.status !== 'incoming' || !call.roomId) return
  getSocket()?.sendCallLeave(call.roomId)
  pendingOffer = null
  pendingCandidates = []
  clearCallTimeout()
  call.endCall()
}

/** Hang up (or cancel) the current call, from either side. */
export function hangUp(): void {
  const call = useCall.getState()
  if (call.status === 'idle') return
  if (call.roomId) getSocket()?.sendCallLeave(call.roomId)
  teardown()
  call.endCall()
}

/** Toggle the microphone; only meaningful once media is flowing. */
export function toggleMute(): void {
  const call = useCall.getState()
  if (call.status !== 'connecting' && call.status !== 'active') return
  const muted = !call.muted
  peer?.setMicMuted(muted)
  call.setMuted(muted)
}
