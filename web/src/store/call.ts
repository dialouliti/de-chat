import { create } from 'zustand'

// State of the 1:1 voice call, driven by features/chat/call-signaling.ts and
// rendered by features/chat/CallView.tsx. Like unread.ts and presence.ts,
// this store holds raw state only — the signaling module owns the WebRTC
// peer, the microphone and the WebSocket writes; it just reports progress
// here so any component can observe the call.

/** Lifecycle of a pair call. */
export type CallStatus =
  | 'idle' // no call at all
  | 'incoming' // a call_offer arrived; ringing, waiting for Accept/Decline
  | 'outgoing' // we started the call; waiting for the peer's answer
  | 'connecting' // offer/answer exchanged; RTCPeerConnection is connecting
  | 'active' // media is flowing (connectionState reached 'connected')
  | 'ended' // terminal; shown briefly, then auto-resets to 'idle'

/** The other party of the call. */
export interface CallPeerInfo {
  userId: string
  username: string
}

// How long the terminal card stays readable before the store resets itself.
const ENDED_RESET_MS = 3000

// Module-level so the timer survives store re-reads and can be cancelled
// when a new call starts within the 'ended' window.
let endedResetTimer: ReturnType<typeof setTimeout> | null = null

function clearEndedReset(): void {
  if (endedResetTimer) {
    clearTimeout(endedResetTimer)
    endedResetTimer = null
  }
}

interface CallState {
  status: CallStatus
  roomId: string | null
  peer: CallPeerInfo | null
  muted: boolean
  /** Epoch ms of when media connected; drives the CallView duration timer. */
  startedAt: number | null
  /** Terminal failure text, shown on the ended card. */
  error: string | null
  /** A call_offer arrived while idle; the user hasn't answered yet. */
  setIncoming: (roomId: string, peer: CallPeerInfo) => void
  /** We initiated a call to `peer`. */
  setOutgoing: (roomId: string, peer: CallPeerInfo) => void
  /** The call was accepted / the answer is being applied. */
  setConnecting: () => void
  /** RTCPeerConnection reached 'connected'; stamps startedAt once. */
  markActive: () => void
  /** Terminal state; `error` (when set) is surfaced on the ended card. */
  endCall: (error?: string | null) => void
  /** Hard reset to 'idle' (auto-reset timer, logout). */
  reset: () => void
  setMuted: (muted: boolean) => void
}

export const useCall = create<CallState>((set, get) => ({
  status: 'idle',
  roomId: null,
  peer: null,
  muted: false,
  startedAt: null,
  error: null,

  setIncoming: (roomId, peer) => {
    clearEndedReset()
    set({ status: 'incoming', roomId, peer, muted: false, startedAt: null, error: null })
  },

  setOutgoing: (roomId, peer) => {
    clearEndedReset()
    set({ status: 'outgoing', roomId, peer, muted: false, startedAt: null, error: null })
  },

  setConnecting: () => set({ status: 'connecting' }),

  markActive: () => {
    // Idempotent: a brief 'disconnected' → 'connected' flap (e.g. a Wi-Fi
    // switch survived by ICE) must not restart the duration timer.
    if (get().status === 'active') return
    set({ status: 'active', startedAt: Date.now() })
  },

  endCall: (error = null) => {
    clearEndedReset()
    set({ status: 'ended', error: error ?? null })
    // The ended card is informational; clear itself out after a moment so
    // the corner is free for the next call. Cancelled by any new start.
    endedResetTimer = setTimeout(() => {
      endedResetTimer = null
      if (useCall.getState().status === 'ended') {
        useCall.getState().reset()
      }
    }, ENDED_RESET_MS)
  },

  reset: () => {
    clearEndedReset()
    set({ status: 'idle', roomId: null, peer: null, muted: false, startedAt: null, error: null })
  },

  setMuted: (muted) => set({ muted }),
}))

/** Selector: true while a call occupies the line ('idle'/'ended' are free). */
export function callInProgress(s: CallState): boolean {
  return s.status !== 'idle' && s.status !== 'ended'
}
