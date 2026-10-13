import { useEffect } from 'react'
import { Navigate, useLocation } from 'react-router-dom'

import { useAuth } from '@/store/auth'
import { initSocket } from '@/ws/socket-store'
import { CallView } from '@/features/chat/CallView'
import { initCallSignaling } from '@/features/chat/call-signaling'

/** Redirects to /login when there's no token. Preserves the intended path. */
export function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const token = useAuth((s) => s.token)
  const location = useLocation()

  // The app-level WebSocket lives as long as the user is authenticated, so
  // unread badges, notifications and presence keep working on every screen.
  // initSocket is idempotent; a changed token (re-login) re-creates the
  // socket internally, and logout closes it via socket-store's own
  // subscription to this store. The call machinery subscribes to the same
  // singleton exactly once (initCallSignaling is idempotent).
  useEffect(() => {
    if (token) {
      initSocket()
      initCallSignaling()
    }
  }, [token])

  if (!token) {
    return <Navigate to="/login" state={{ from: location.pathname }} replace />
  }
  // Every protected route renders its page through here — and only one
  // route matches at a time — so the CallView overlay is effectively
  // mounted once for the whole protected area, whatever screen is on.
  return (
    <>
      {children}
      <CallView />
    </>
  )
}
