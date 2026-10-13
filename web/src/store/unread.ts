import { create } from 'zustand'

// Unread counters per room. Seeded from REST GET /api/v1/me/unread (rooms
// with count > 0 only — the server omits the rest), then kept live by the
// app-level WebSocket stream in ws/socket-store: messages in rooms the user
// isn't looking at increment, opening a room clears.
//
// The total is NOT stored — components derive it with selectUnreadTotal.

/** One entry of the GET /api/v1/me/unread response (models.RoomUnread). */
export interface UnreadEntry {
  room_id: string
  unread_count: number
}

interface UnreadState {
  /** room_id → unread count; only rooms with count > 0 are present. */
  counts: Record<string, number>
  /** Replace the whole map from a fresh REST response. */
  setAll: (entries: UnreadEntry[]) => void
  /** +1 for a live message in a room the user isn't looking at. */
  increment: (roomId: string) => void
  /** Zero a room's counter (the room was opened / marked read). */
  clear: (roomId: string) => void
}

export const useUnread = create<UnreadState>((set) => ({
  counts: {},
  setAll: (entries) =>
    set({
      counts: Object.fromEntries(
        entries
          .filter((e) => e.unread_count > 0)
          .map((e) => [e.room_id, e.unread_count]),
      ),
    }),
  increment: (roomId) =>
    set((s) => ({ counts: { ...s.counts, [roomId]: (s.counts[roomId] ?? 0) + 1 } })),
  clear: (roomId) =>
    set((s) => {
      if (s.counts[roomId] === undefined) return s
      const next = { ...s.counts }
      delete next[roomId]
      return { counts: next }
    }),
}))

/** Selector: total unread across all rooms (for the tab-title badge). */
export const selectUnreadTotal = (s: UnreadState): number =>
  Object.values(s.counts).reduce((sum, n) => sum + n, 0)

// --- Tab title badge --------------------------------------------------------
// Mirrors the base <title> from index.html. Module-level subscription (not a
// hook effect) so it keeps working for as long as the tab is open, no matter
// which page is mounted — the same module-load side-effect style as auth.ts
// hydration.
const BASE_TITLE = 'deChat'

useUnread.subscribe((state) => {
  const total = selectUnreadTotal(state)
  document.title = total > 0 ? `(${total}) ${BASE_TITLE}` : BASE_TITLE
})
