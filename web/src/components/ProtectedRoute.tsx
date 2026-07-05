import { Navigate, useLocation } from 'react-router-dom'

import { useAuth } from '@/store/auth'

/** Redirects to /login when there's no token. Preserves the intended path. */
export function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const token = useAuth((s) => s.token)
  const location = useLocation()

  if (!token) {
    return <Navigate to="/login" state={{ from: location.pathname }} replace />
  }
  return <>{children}</>
}
