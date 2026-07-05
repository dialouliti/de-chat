import { Link, useNavigate } from 'react-router-dom'

import { useAuth } from '@/store/auth'

/** App shell: top bar with brand, current user, and logout. */
export function Layout({ children }: { children: React.ReactNode }) {
  const { username, role, logout } = useAuth()
  const navigate = useNavigate()

  function handleLogout() {
    logout()
    navigate('/login', { replace: true })
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center justify-between border-b border-slate-800 bg-slate-900 px-4 py-2">
        <Link to="/rooms" className="text-lg font-semibold text-slate-100">
          deChat
        </Link>
        <div className="flex items-center gap-3 text-sm text-slate-400">
          <span>
            {username}
            {role === 'admin' && (
              <span className="ml-1 rounded bg-slate-700 px-1.5 py-0.5 text-xs text-slate-200">
                admin
              </span>
            )}
          </span>
          <button
            onClick={handleLogout}
            className="rounded px-2 py-1 text-slate-300 hover:bg-slate-800 hover:text-white"
          >
            Logout
          </button>
        </div>
      </header>
      <main className="flex-1 overflow-hidden">{children}</main>
    </div>
  )
}
