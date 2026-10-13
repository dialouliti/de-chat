// The application-level ChatSocket singleton.
//
// useChatRoom used to create a socket per room-page mount, which meant every
// cross-cutting concern tied to the live stream — unread badges, browser
// notifications, presence — went dead the moment the user left a chat room.
// The server pushes all rooms down a single connection anyway (see
// internal/hub/hub.go), so there is exactly one socket for the whole app and
// it lives here:
//
//   - initSocket()    — called by ProtectedRoute once the user is
//                       authenticated; idempotent, and re-creates the socket
//                       when the token changes (re-login);
//   - closeSocket()   — logout, via the lazy auth-store subscription below;
//   - subscribe()     — room-scoped listeners (useChatRoom today, the call UI
//                       later) that run after the global side effects;
//   - setActiveRoom() — which room is on screen, driving unread counting;
//   - useSocketStatus() and the presence store — shared React-facing state.
//
// Lifecycle note: auth.ts must NOT import this module — socket.ts already
// imports auth, so auth → socket-store → socket → auth would be a module
// cycle. Instead the auth store is subscribed to lazily from here, on the
// first initSocket() call; importing socket-store alone has no side effects.

import { create } from 'zustand'

import { apiClient } from '@/api/client'
import { getToken, useAuth } from '@/store/auth'
import { usePresence } from '@/store/presence'
import { useUnread, type UnreadEntry } from '@/store/unread'
import { showNotification } from '@/lib/notifications'
import { ChatSocket, type ServerMessage, type ServerMessageHandler } from './socket'

export type ConnectionStatus = 'connecting' | 'open' | 'closed'

// --- Connection status --------------------------------------------------------
// A tiny dedicated store so React components can hook into the socket status
// while the singleton itself stays framework-free.

interface SocketStatusState {
  status: ConnectionStatus
  setStatus: (status: ConnectionStatus) => void
}

const useSocketStatusStore = create<SocketStatusState>((set) => ({
  status: 'closed',
  setStatus: (status) => set({ status }),
}))

/** The app-level socket's connection status, as a React hook. */
export function useSocketStatus(): ConnectionStatus {
  return useSocketStatusStore((s) => s.status)
}

// --- Singleton state ----------------------------------------------------------

let socket: ChatSocket | null = null
/** The token the current socket was opened with; mismatch means re-login. */
let socketToken: string | null = null
/** The room whose page is on screen right now; null outside chat rooms. */
let activeRoom: string | null = null
/** Room-scoped listeners, fanned out after the global side effects. */
const handlers = new Set<ServerMessageHandler>()

function fanout(m: ServerMessage): void {
  for (const h of handlers) h(m)
}

/**
 * Cross-cutting side effects that must run no matter which screen is
 * mounted. Runs before fanout so subscribers always observe the message
 * after its global consequences (unread bump, presence update) have landed.
 */
function handleGlobally(m: ServerMessage): void {
  switch (m.type) {
    case 'message': {
      const msg = m.message
      if (!msg) return
      // Own messages never count as unread (matches the server, which
      // excludes the caller's messages from GET /me/unread). Usernames
      // are unique in deChat, so a username comparison identifies the
      // author reliably.
      const own = useAuth.getState().username === msg.username
      if (!own && msg.room_id !== activeRoom) {
        // Message for a room the user isn't looking at — bump its badge.
        useUnread.getState().increment(msg.room_id)
      }
      // Desktop notification for other people's messages. The tab is most
      // likely in the background, but showNotification re-checks
      // document.hidden itself — this also covers reading one room while
      // messages land in another.
      if (!own) showNotification(msg.username, msg.content)
      return
    }

    case 'presence': {
      const presence = usePresence.getState()
      if (m.online_users) {
        // Initial snapshot delivered to the newcomer.
        presence.setAll(m.online_users)
      }
      presence.apply({ user_id: m.user_id, username: m.username }, m.is_online)
      return
    }

    default:
      return
  }
}

function destroySocket(): void {
  socket?.close()
  socket = null
  socketToken = null
  useSocketStatusStore.getState().setStatus('closed')
  // The online list was observed under the old session; the next connect
  // delivers a fresh snapshot.
  usePresence.getState().reset()
}

// --- Unread badge restore -----------------------------------------------------
// handleGlobally counts unread only from messages that arrive over the live
// socket — whatever was sent while the app was disconnected never fires it,
// so after every outage the badges undercount. The server's read-cursor
// table is the source of truth, so each transition to 'open' (the first
// connect and every reconnect) re-syncs the counters from GET /me/unread.

let unreadSyncInFlight = false

function restoreUnread(): void {
  // 'open' can fire in quick succession on a flappy connection; skipping
  // keeps responses from racing each other into setAll out of order.
  if (unreadSyncInFlight) return
  unreadSyncInFlight = true
  apiClient
    .GET('/api/v1/me/unread', {})
    .then(({ data, error }) => {
      // Best-effort: on failure the last known counts stay, and the next
      // reconnect retries.
      if (error || !data) return
      useUnread.getState().setAll((data as unknown as UnreadEntry[]) ?? [])
    })
    .catch(() => {
      // A network failure right after a reconnect is expected; nothing to
      // recover here.
    })
    .finally(() => {
      unreadSyncInFlight = false
    })
}

// --- Auth lifecycle -----------------------------------------------------------

let watchingAuth = false

/** Register the auth-store subscription at most once, on first use. */
function watchAuth(): void {
  if (watchingAuth) return
  watchingAuth = true
  useAuth.subscribe((state, prev) => {
    if (state.token === prev.token) return
    if (state.token) initSocket() // re-login with a fresh token
    else closeSocket() // logout — drop the connection and forget it
  })
}

// --- Public API ---------------------------------------------------------------

/**
 * Create the app-level socket if it doesn't exist (or re-create it after a
 * token change) and connect. Idempotent: with an unchanged token this is a
 * no-op — the socket reconnects on its own after transport drops.
 */
export function initSocket(): void {
  watchAuth()

  const token = getToken()
  if (!token) return // nothing to authenticate with yet

  if (socket && socketToken === token) return

  // A different token means the previous socket is stale: its URL and any
  // in-flight state are bound to the old JWT.
  if (socket) destroySocket()

  socketToken = token
  socket = new ChatSocket({
    onMessage: (m) => {
      handleGlobally(m)
      fanout(m)
    },
    onStatus: (s) => {
      useSocketStatusStore.getState().setStatus(s)
      // Reconnects are invisible to the live unread counting (no WS events
      // were seen while down) — resync the badges from the server.
      if (s === 'open') restoreUnread()
    },
  })
  socket.connect()
}

/** Close the socket and forget it (logout). Listener subscriptions are not
 * touched — they manage their own lifecycle. */
export function closeSocket(): void {
  if (!socket && socketToken === null) return
  destroySocket()
}

/**
 * The current app-level socket, or null before the first initSocket / after
 * closeSocket. Senders (useChatRoom, the call UI) go through this.
 */
export function getSocket(): ChatSocket | null {
  return socket
}

/** Subscribe to every incoming server message. Returns an unsubscribe fn. */
export function subscribe(h: ServerMessageHandler): () => void {
  handlers.add(h)
  return () => {
    handlers.delete(h)
  }
}

/**
 * Tell the singleton which room's page is on screen (null outside chat
 * rooms). This is what the unread counting in handleGlobally keys off.
 */
export function setActiveRoom(roomId: string | null): void {
  activeRoom = roomId
}
