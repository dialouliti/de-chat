// Audio-only 1:1 WebRTC peer implementing canonical perfect negotiation
// (the MDN pattern) on top of an injected signaling transport.
//
// The consumer owns the WebSocket and the call state — CallPeer never
// touches app stores. Signaling callbacks carry JSON-stringified session
// descriptions and candidate objects, matching the `content` payloads of
// the chat's call_offer / call_answer / call_ice messages (see ws/socket.ts).
//
// Perfect-negotiation roles: `polite` must be derived deterministically by
// the consumer, e.g. by comparing the two user ids (the lexicographically
// smaller one is polite), so both peers compute complementary roles without
// extra signaling. Exactly one side ends up impolite.

/** Constructor options for CallPeer. */
export interface CallPeerOptions {
  /** STUN/TURN list passed straight to the RTCPeerConnection config. */
  iceServers: RTCIceServer[]
  /**
   * Perfect-negotiation role: on an offer collision the polite peer yields
   * (rolls its own offer back) while the impolite peer ignores the incoming
   * offer. Derived by the consumer from a stable ordering of the user ids.
   */
  polite: boolean
  /** Called with a JSON-stringified local offer to send over signaling. */
  sendOffer(sdp: string): void
  /** Called with a JSON-stringified local answer to send over signaling. */
  sendAnswer(sdp: string): void
  /** Called with each local ICE candidate gathered by this peer. */
  sendCandidate(candidate: RTCIceCandidateInit): void
  /** Fires once when the remote audio stream first arrives. */
  onRemoteStream?(stream: MediaStream): void
  /** Fires on every RTCPeerConnection state change. */
  onStateChange?(state: RTCPeerConnectionState): void
  /** Soft error channel — failures are swallowed, never rethrown. */
  onError?(message: string): void
}

// Payload type of the opus codec in an "a=rtpmap:111 opus/48000/2" line.
const RTPMAP_OPUS_RE = /^a=rtpmap:(\d+)\s+opus\//i
// Any fmtp line; capture 1 is the payload type it configures.
const FMTP_RE = /^a=fmtp:(\d+)\s*(.*)$/

/**
 * Best-effort, idempotent SDP munging: appends ";usedtx=1;useinbandfec=1"
 * (Opus discontinuous transmission + in-band FEC) to the fmtp line of every
 * opus codec. On anything unexpected the original SDP is returned untouched.
 */
function applyOpusExtensions(sdp: string): string {
  try {
    const eol = sdp.includes('\r\n') ? '\r\n' : '\n'
    const lines = sdp.split(eol)

    const opusPayloadTypes = new Set<string>()
    for (const line of lines) {
      const pt = RTPMAP_OPUS_RE.exec(line)?.[1]
      if (pt !== undefined) opusPayloadTypes.add(pt)
    }
    if (opusPayloadTypes.size === 0) return sdp

    return lines
      .map((line) => {
        const fmtp = FMTP_RE.exec(line)
        const pt = fmtp?.[1]
        if (pt === undefined || !opusPayloadTypes.has(pt)) return line

        // Firefox already ships usedtx/useinbandfec — only add what is
        // missing so repeated munging stays a no-op.
        let params = (fmtp?.[2] ?? '').trim().replace(/;+$/, '')
        const additions: string[] = []
        if (!/usedtx\s*=/i.test(params)) additions.push('usedtx=1')
        if (!/useinbandfec\s*=/i.test(params)) additions.push('useinbandfec=1')
        if (additions.length === 0) return line
        params = params ? `${params};${additions.join(';')}` : additions.join(';')
        return `a=fmtp:${pt} ${params}`
      })
      .join(eol)
  } catch {
    return sdp
  }
}

/** Parse and validate a JSON-string session description. */
function parseDescription(
  sdpJson: string,
  expectedType: 'offer' | 'answer',
): RTCSessionDescriptionInit | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(sdpJson)
  } catch {
    return null
  }
  if (typeof parsed !== 'object' || parsed === null) return null
  const desc = parsed as { type?: unknown; sdp?: unknown }
  if (desc.type !== expectedType || typeof desc.sdp !== 'string') return null
  return { type: expectedType, sdp: desc.sdp }
}

export class CallPeer {
  private readonly opts: CallPeerOptions
  private readonly pc: RTCPeerConnection

  // Glare-control flags, as in the MDN perfect-negotiation example.
  private makingOffer = false
  private ignoreOffer = false
  private isSettingRemoteAnswerPending = false

  /** Remote candidates that arrived before the remote description was set. */
  private readonly pendingCandidates: RTCIceCandidateInit[] = []
  /** Aggregate stream for peers that add tracks without a MediaStream. */
  private remoteStream: MediaStream | null = null
  private remoteStreamAnnounced = false
  private closed = false

  constructor(opts: CallPeerOptions) {
    this.opts = opts
    this.pc = new RTCPeerConnection({ iceServers: opts.iceServers })
    this.attachHandlers()
  }

  /**
   * Adds the local microphone tracks. addTrack associates each track with
   * the stream, so the remote peer receives it in ontrack's
   * event.streams[0]; direction is sendrecv (the addTrack default).
   */
  addMicStream(stream: MediaStream): void {
    for (const track of stream.getTracks()) {
      this.pc.addTrack(track, stream)
    }
  }

  /** Feed a JSON-stringified remote offer (from call_offer messages). */
  handleOffer(sdpJson: string): void {
    void this.processOffer(sdpJson)
  }

  /** Feed a JSON-stringified remote answer (from call_answer messages). */
  handleAnswer(sdpJson: string): void {
    void this.processAnswer(sdpJson)
  }

  /** Feed a remote ICE candidate (from call_ice messages). */
  handleCandidate(candidate: RTCIceCandidateInit): void {
    if (this.closed) return
    if (!this.pc.remoteDescription) {
      // addIceCandidate rejects before a remote description exists — queue
      // instead; the queue is drained right after setRemoteDescription.
      this.pendingCandidates.push(candidate)
      return
    }
    void this.addRemoteCandidate(candidate)
  }

  /**
   * Request an ICE restart (e.g. after connectionState 'failed' or a network
   * change). The renegotiation flows back through the regular
   * negotiationneeded → offer pipeline.
   *
   * Only the IMPOLITE side should initiate a restart: simultaneous restarts
   * glare, and the polite side would then roll back and lose its restart.
   */
  restartIce(): void {
    if (this.closed) return
    this.pc.restartIce()
  }

  /** Mute or unmute the microphone by toggling every audio sender's track. */
  setMicMuted(muted: boolean): void {
    for (const sender of this.pc.getSenders()) {
      if (sender.track?.kind === 'audio') {
        sender.track.enabled = !muted
      }
    }
  }

  /** Detach handlers and close the underlying connection. */
  close(): void {
    if (this.closed) return
    this.closed = true
    this.pendingCandidates.length = 0
    this.remoteStream = null
    this.remoteStreamAnnounced = false
    this.pc.onnegotiationneeded = null
    this.pc.onicecandidate = null
    this.pc.ontrack = null
    this.pc.onconnectionstatechange = null
    this.pc.close()
  }

  // ------------------------------------------------------------------ setup

  private attachHandlers(): void {
    this.pc.onnegotiationneeded = () => {
      void this.negotiate()
    }

    this.pc.onicecandidate = ({ candidate }) => {
      if (candidate) this.opts.sendCandidate(candidate.toJSON())
    }

    this.pc.ontrack = (event) => {
      const associated = event.streams[0]
      const stream = associated ?? this.fallbackRemoteStream()
      if (associated === undefined) stream.addTrack(event.track)
      if (!this.remoteStreamAnnounced) {
        this.remoteStreamAnnounced = true
        this.opts.onRemoteStream?.(stream)
      }
    }

    this.pc.onconnectionstatechange = () => {
      this.opts.onStateChange?.(this.pc.connectionState)
    }
  }

  /** Lazily-created aggregate stream for streams-less remote tracks. */
  private fallbackRemoteStream(): MediaStream {
    this.remoteStream ??= new MediaStream()
    return this.remoteStream
  }

  // -------------------------------------------------------- signaling path

  /** negotiationneeded: create an offer, munge it, ship it. */
  private async negotiate(): Promise<void> {
    if (this.closed) return
    try {
      this.makingOffer = true
      const offer = await this.pc.createOffer()
      if (offer.sdp !== undefined) offer.sdp = applyOpusExtensions(offer.sdp)
      await this.pc.setLocalDescription(offer)
      this.opts.sendOffer(JSON.stringify(this.pc.localDescription))
    } catch (err) {
      this.reportError('negotiation failed', err)
    } finally {
      this.makingOffer = false
    }
  }

  private async processOffer(sdpJson: string): Promise<void> {
    if (this.closed) return
    const description = parseDescription(sdpJson, 'offer')
    if (!description) {
      this.opts.onError?.('received a malformed offer')
      return
    }

    // Glare: an offer arrives while our own offer is in flight or we are
    // not stable. The impolite side ignores it; the polite side yields and
    // takes the remote offer below.
    const readyForOffer =
      !this.makingOffer &&
      (this.pc.signalingState === 'stable' || this.isSettingRemoteAnswerPending)
    const offerCollision = !readyForOffer

    this.ignoreOffer = !this.opts.polite && offerCollision
    if (this.ignoreOffer) return

    try {
      if (offerCollision) {
        // Polite rollback of our half-finished offer. Best-effort, exactly
        // like the MDN example — a rollback failure is swallowed and we
        // still try to take the remote offer.
        try {
          await this.pc.setLocalDescription({ type: 'rollback' })
        } catch {
          // fall through to setRemoteDescription
        }
      }

      await this.pc.setRemoteDescription(description)
      await this.drainPendingCandidates()

      const answer = await this.pc.createAnswer()
      if (answer.sdp !== undefined) answer.sdp = applyOpusExtensions(answer.sdp)
      await this.pc.setLocalDescription(answer)
      this.opts.sendAnswer(JSON.stringify(this.pc.localDescription))
    } catch (err) {
      this.reportError('failed to apply offer', err)
    }
  }

  private async processAnswer(sdpJson: string): Promise<void> {
    if (this.closed) return
    const description = parseDescription(sdpJson, 'answer')
    if (!description) {
      this.opts.onError?.('received a malformed answer')
      return
    }

    try {
      // Guards the glare race: while an answer is being applied,
      // negotiationneeded must not be mistaken for a fresh offer.
      this.isSettingRemoteAnswerPending = true
      await this.pc.setRemoteDescription(description)
    } catch (err) {
      // Stale answers (e.g. left over from a rolled-back offer) are
      // dropped, as in the MDN example.
      this.reportError('failed to apply answer', err)
      return
    } finally {
      this.isSettingRemoteAnswerPending = false
    }

    await this.drainPendingCandidates()
  }

  private async drainPendingCandidates(): Promise<void> {
    while (this.pendingCandidates.length > 0) {
      const candidate = this.pendingCandidates.shift()
      if (candidate === undefined) break
      await this.addRemoteCandidate(candidate)
    }
  }

  private async addRemoteCandidate(candidate: RTCIceCandidateInit): Promise<void> {
    try {
      await this.pc.addIceCandidate(candidate)
    } catch (err) {
      // Candidates belonging to an ignored offer are expected to fail —
      // suppress them silently (MDN: "if (!ignoreOffer) throw"). Any other
      // late candidate (e.g. after an ICE restart) is dropped too: it must
      // never take the call down.
      if (!this.ignoreOffer) this.reportError('failed to add ICE candidate', err)
    }
  }

  private reportError(context: string, err: unknown): void {
    const detail = err instanceof Error ? err.message : String(err)
    this.opts.onError?.(`${context}: ${detail}`)
  }
}
