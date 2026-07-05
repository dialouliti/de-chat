import { create } from 'zustand'

// The persisted slice — only what's needed to restore a session without
// re-prompting for login. The role is cached so the UI can render before
// /me is re-fetched; it's revalidated on the next authenticated call.
interface PersistedAuth {
  token: string
  username: string
  role: string
}

const STORAGE_KEY = 'dechat.auth'

function loadPersisted(): PersistedAuth | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as PersistedAuth
    if (!parsed.token) return null
    return parsed
  } catch {
    return null
  }
}

function savePersisted(a: PersistedAuth | null) {
  try {
    if (a) localStorage.setItem(STORAGE_KEY, JSON.stringify(a))
    else localStorage.removeItem(STORAGE_KEY)
  } catch {
    // localStorage may be unavailable (private mode); fail silently. The app
    // still works for the session, just without persistence.
  }
}

interface AuthState {
  token: string | null
  username: string | null
  role: string | null
  /** Marks whether we've attempted initial load from localStorage. */
  hydrated: boolean
  login: (a: PersistedAuth) => void
  logout: () => void
}

export const useAuth = create<AuthState>((set) => ({
  token: null,
  username: null,
  role: null,
  hydrated: false,
  login: (a) => {
    savePersisted(a)
    set({ token: a.token, username: a.username, role: a.role, hydrated: true })
  },
  logout: () => {
    savePersisted(null)
    set({ token: null, username: null, role: null, hydrated: true })
  },
}))

// Hydrate synchronously from localStorage on module load so the very first
// render knows whether to show Login or the app. The API client reads through
// getToken() below.
const initial = loadPersisted()
if (initial) {
  useAuth.setState({
    token: initial.token,
    username: initial.username,
    role: initial.role,
    hydrated: true,
  })
} else {
  useAuth.setState({ hydrated: true })
}

/**
 * Non-React accessor for the current token. Used by the API client's fetch
 * middleware and by the WebSocket URL builder, both of which run outside
 * React's render cycle.
 */
export function getToken(): string | null {
  return useAuth.getState().token
}
