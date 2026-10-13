import { create } from 'zustand'

import type { PresenceUser } from '@/ws/socket'

// Who is online, according to the app-level WebSocket. The socket-store
// (web/src/ws/socket-store.ts) is the only writer: on every "presence" event
// it applies the connect-time snapshot (online_users) and then the per-user
// deltas. Screens read the list via usePresence() — the chat room header
// today, member lists and the call UI later.
//
// Like unread.ts, the store holds raw state only; all merging/dedup logic
// lives in the actions so callers never hand-edit the array.

interface PresenceState {
  /** Currently online users, deduplicated by user_id. */
  users: PresenceUser[]
  /** Replace the whole list — the snapshot delivered on connect. */
  setAll: (users: PresenceUser[]) => void
  /** Apply a single user's online/offline delta. */
  apply: (user: PresenceUser, isOnline: boolean) => void
  /** Drop everything — the socket is gone (logout / re-login). */
  reset: () => void
}

export const usePresence = create<PresenceState>((set) => ({
  users: [],
  setAll: (users) => set({ users }),
  apply: (user, isOnline) =>
    set((s) => {
      if (isOnline) {
        if (s.users.some((u) => u.user_id === user.user_id)) return s
        return { users: [...s.users, user] }
      }
      return { users: s.users.filter((u) => u.user_id !== user.user_id) }
    }),
  reset: () => set({ users: [] }),
}))
