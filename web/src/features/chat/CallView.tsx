import { useEffect, useState } from 'react'

import { useCall } from '@/store/call'
import { answerCall, declineCall, hangUp, toggleMute } from './call-signaling'

// The call overlay: a small fixed card in the bottom-right corner showing
// whatever the call store says — incoming (Accept/Decline), outgoing
// (Calling…/Cancel), active (duration, Mute, Hang up) or the terminal card
// for a few seconds. Mounted once alongside the protected area's content
// (see ProtectedRoute), so it stays on screen across navigation.

/** Live mm:ss elapsed since `startedAt`; ticks once per second while active. */
function useCallDuration(startedAt: number | null, active: boolean): string {
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (!active || startedAt === null) return
    setNow(Date.now())
    const timer = setInterval(() => setNow(Date.now()), 1000)
    // Cleared on every dependency change AND on unmount — the store resets
    // startedAt when the call ends, but the component may also go away
    // first (logout); neither may leave the interval running.
    return () => clearInterval(timer)
  }, [active, startedAt])

  if (startedAt === null) return '00:00'
  const elapsed = Math.max(0, Math.floor((now - startedAt) / 1000))
  const mm = String(Math.floor(elapsed / 60)).padStart(2, '0')
  const ss = String(elapsed % 60).padStart(2, '0')
  return `${mm}:${ss}`
}

export function CallView() {
  const status = useCall((s) => s.status)
  const peer = useCall((s) => s.peer)
  const muted = useCall((s) => s.muted)
  const startedAt = useCall((s) => s.startedAt)
  const error = useCall((s) => s.error)
  const duration = useCallDuration(startedAt, status === 'active')

  if (status === 'idle') return null

  return (
    <div className="fixed bottom-4 right-4 z-50 w-72 rounded-xl border border-slate-800 bg-slate-900/95 p-4 shadow-lg backdrop-blur">
      {status === 'incoming' && (
        <>
          <p className="text-xs uppercase tracking-wide text-slate-500">Incoming call</p>
          <h3 className="mt-1 text-lg font-semibold text-slate-100">{peer?.username}</h3>
          <div className="mt-3 flex gap-2">
            <button
              onClick={() => void answerCall()}
              className="flex-1 rounded-lg bg-green-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-green-500"
            >
              Accept
            </button>
            <button
              onClick={declineCall}
              className="flex-1 rounded-lg bg-red-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-red-500"
            >
              Decline
            </button>
          </div>
        </>
      )}

      {status === 'outgoing' && (
        <>
          <p className="text-xs uppercase tracking-wide text-slate-500">Calling…</p>
          <h3 className="mt-1 text-lg font-semibold text-slate-100">{peer?.username}</h3>
          <div className="mt-3 flex gap-2">
            <button
              onClick={hangUp}
              className="flex-1 rounded-lg bg-red-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-red-500"
            >
              Cancel
            </button>
          </div>
        </>
      )}

      {(status === 'connecting' || status === 'active') && (
        <>
          <div className="flex items-baseline justify-between">
            <h3 className="text-lg font-semibold text-slate-100">{peer?.username}</h3>
            <span className="font-mono text-sm text-slate-400">
              {status === 'active' ? duration : 'connecting…'}
            </span>
          </div>
          <div className="mt-3 flex gap-2">
            <button
              onClick={toggleMute}
              className={`flex-1 rounded-lg px-3 py-1.5 text-sm font-medium ${
                muted
                  ? 'bg-amber-600 text-white hover:bg-amber-500'
                  : 'bg-slate-700 text-slate-100 hover:bg-slate-600'
              }`}
            >
              {muted ? 'Unmute' : 'Mute'}
            </button>
            <button
              onClick={hangUp}
              className="flex-1 rounded-lg bg-red-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-red-500"
            >
              Hang up
            </button>
          </div>
        </>
      )}

      {status === 'ended' && (
        <>
          <h3 className="text-lg font-semibold text-slate-100">Call ended</h3>
          <p className="mt-1 min-h-[1.25rem] text-sm text-red-400">{error}</p>
        </>
      )}
    </div>
  )
}
