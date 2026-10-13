// useChatRoom ties together the REST message history and the live WebSocket
// stream for a single room.
//
// The server broadcasts every message to every connected client (see
// internal/hub/hub.go) — there is no server-side per-room subscription in v1.
// So the client filters incoming WS messages by the active room_id locally.
//
// The socket itself is NOT owned by this hook anymore: it is the app-level
// singleton from socket-store.ts (created by ProtectedRoute once the user is
// authenticated, closed on logout). The hook subscribes on mount, reads the
// shared connection status and presence list, reports the open room via
// setActiveRoom, and sends through getSocket — so unread badges and
// notifications keep working on every screen, not just inside a room.

import { useCallback, useEffect, useRef, useState } from 'react'

import { apiClient } from '@/api/client'
import { useUnread } from '@/store/unread'
import { usePresence } from '@/store/presence'
import {
  getSocket,
  setActiveRoom,
  subscribe,
  useSocketStatus,
  type ConnectionStatus,
} from './socket-store'
import type { ReactionEntry, ServerMessage } from './socket'

export type ChatMessage = {
  id: string
  room_id: string
  user_id: string
  username: string
  content: string
  created_at: string
  edited_at?: string | null
  /** Aggregated reactions from the history payload; may be absent. */
  reactions?: ReactionEntry[]
}

export type ReadReceipt = {
  user_id: string
  username: string
  last_read_at: string
}

// Re-exported for backwards compatibility: the type used to live here and
// UseChatRoomResult still references it.
export type { ConnectionStatus }

export interface UseChatRoomResult {
  messages: ChatMessage[]
  status: ConnectionStatus
  send: (content: string) => boolean
  edit: (messageId: string, content: string) => boolean
  react: (messageId: string, emoji: string) => boolean
  sendTyping: (isTyping: boolean) => void
  markRead: () => void
  lastError: string | null
  typingUsers: string[]
  onlineUsers: { user_id: string; username: string }[]
  readReceipts: ReadReceipt[]
}

// How long after the last "typing" event we clear the indicator locally, in
// case the server's "stopped typing" message is missed (e.g. disconnect).
const TYPING_TIMEOUT_MS = 4000

// Upper bound for a single backfill page request. A response that never
// arrives must not stall the serialized backfill queue (and with it every
// later reconnect's gap fill) for as long as the room page stays mounted.
const BACKFILL_FETCH_TIMEOUT_MS = 15_000

export function useChatRoom(roomId: string | null): UseChatRoomResult {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [lastError, setLastError] = useState<string | null>(null)
  const [typingUsers, setTypingUsers] = useState<string[]>([])
  const [readReceipts, setReadReceipts] = useState<ReadReceipt[]>([])

  // Connection status and presence belong to the app-level socket: they
  // outlive this component (and this room).
  const status = useSocketStatus()
  const onlineUsers = usePresence((s) => s.users)

  const activeRoomRef = useRef<string | null>(roomId)
  const typingTimeouts = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map())

  // created_at of the newest message this client knows for the active room —
  // the cursor the reconnect backfill resumes from. null until the first
  // history page (or a live message) establishes it; reset on room switch.
  const highWaterMark = useRef<string | null>(null)
  // Backfill loops are serialized through this promise tail. When the socket
  // re-opens while a loop is still awaiting its response, the new trigger
  // must neither race it nor be dropped (a dropped trigger would leave the
  // newer outage's gap unfilled) — it waits its turn and then walks the
  // range from the current mark, which by then covers both gaps.
  const backfillQueue = useRef<Promise<void>>(Promise.resolve())

  // Keep the ref current so the WS callback always sees the latest room, and
  // tell the singleton which room is on screen — that is what its unread
  // counting and notification logic key off. Both reset when the room page
  // goes away, so messages in a room nobody watches count as unread again.
  useEffect(() => {
    activeRoomRef.current = roomId
    setActiveRoom(roomId)
    return () => {
      activeRoomRef.current = null
      setActiveRoom(null)
    }
  }, [roomId])

  // Advance the high-water mark; never regress it. A live broadcast is newer
  // than everything known by construction, but a backfill started earlier
  // can finish later with an older tail, so every writer takes the max.
  // Server timestamps are RFC 3339 with nanosecond fractions of varying
  // length (trailing zeros trimmed), which don't order lexicographically —
  // compare as instants.
  const advanceHighWaterMark = useCallback((createdAt: string) => {
    const prev = highWaterMark.current
    if (prev === null || Date.parse(createdAt) > Date.parse(prev)) {
      highWaterMark.current = createdAt
    }
  }, [])

  // Helper: schedule auto-clear of a typing indicator so a missed stop event
  // doesn't leave a stale "typing…" forever.
  const scheduleTypingClear = useCallback((username: string) => {
    const prev = typingTimeouts.current.get(username)
    if (prev) clearTimeout(prev)
    const t = setTimeout(() => {
      setTypingUsers((prev) => prev.filter((u) => u !== username))
      typingTimeouts.current.delete(username)
    }, TYPING_TIMEOUT_MS)
    typingTimeouts.current.set(username, t)
  }, [])

  const handleServerMessage = useCallback(
    (m: ServerMessage) => {
      switch (m.type) {
        case 'pong':
          return

        case 'error':
          setLastError(m.text ?? 'unknown error')
          return

        case 'message': {
          const msg = m.message
          if (!msg) return
          // Unread badges and desktop notifications are handled globally in
          // socket-store (see handleGlobally there); here we only render the
          // messages of the room that is on screen.
          if (activeRoomRef.current && msg.room_id === activeRoomRef.current) {
            // Keep the reconnect cursor ahead of the live stream so a
            // backfill never has to re-fetch what WS already delivered.
            advanceHighWaterMark(msg.created_at)
            setMessages((prev) => {
              if (prev.some((p) => p.id === msg.id)) return prev
              return [...prev, msg as ChatMessage]
            })
          }
          return
        }

        case 'message_edited':
          if (m.message && activeRoomRef.current && m.message.room_id === activeRoomRef.current) {
            setMessages((prev) =>
              prev.map((p) =>
                p.id === m.message!.id
                  ? // The edit response omits `reactions` (omitempty on the
                    // server), so keep the locally known ones — edits don't
                    // touch reactions.
                    { ...(m.message as ChatMessage), reactions: m.message.reactions ?? p.reactions }
                  : p,
              ),
            )
          }
          return

        case 'reaction': {
          if (!activeRoomRef.current || m.room_id !== activeRoomRef.current) return
          const entry: ReactionEntry = {
            emoji: m.emoji,
            user_id: m.user_id,
            username: m.username,
          }
          setMessages((prev) =>
            prev.map((p) => {
              if (p.id !== m.message_id) return p
              const has = (p.reactions ?? []).some(
                (r) => r.emoji === entry.emoji && r.user_id === entry.user_id,
              )
              if (m.reaction_added) {
                if (has) return p
                return { ...p, reactions: [...(p.reactions ?? []), entry] }
              }
              if (!has) return p
              return {
                ...p,
                reactions: (p.reactions ?? []).filter(
                  (r) => !(r.emoji === entry.emoji && r.user_id === entry.user_id),
                ),
              }
            }),
          )
          return
        }

        case 'typing':
          if (activeRoomRef.current && m.room_id === activeRoomRef.current) {
            if (m.is_typing) {
              setTypingUsers((prev) => (prev.includes(m.username) ? prev : [...prev, m.username]))
              scheduleTypingClear(m.username)
            } else {
              setTypingUsers((prev) => prev.filter((u) => u !== m.username))
              const t = typingTimeouts.current.get(m.username)
              if (t) {
                clearTimeout(t)
                typingTimeouts.current.delete(m.username)
              }
            }
          }
          return

        case 'presence':
          // Maintained globally by socket-store into the presence store;
          // this hook reads it via usePresence above.
          return

        case 'read_receipt':
          if (activeRoomRef.current && m.room_id === activeRoomRef.current) {
            setReadReceipts(m.read_by)
          }
          return

        // Call signalling (voice). Deliberately ignored here — the call UI
        // arrives in a later task. The cases are explicit (rather than a
        // silent fall-through past the switch) so this handler visibly
        // acknowledges every member of the ServerMessage union in socket.ts;
        // when the call UI lands, these are the hooks to fill in.
        case 'call_join':
        case 'call_leave':
        case 'call_offer':
        case 'call_answer':
        case 'call_ice':
          return
      }
    },
    [scheduleTypingClear, advanceHighWaterMark],
  )

  // Tap into the app-level socket for as long as this room is mounted. The
  // hook never creates or closes the socket itself.
  useEffect(() => subscribe(handleServerMessage), [handleServerMessage])

  // Load history when entering a room. Switching rooms does NOT touch the
  // socket — the server pushes all rooms down the one app-level connection.
  useEffect(() => {
    let cancelled = false
    // The mark is per-room: the previous room's newest message says nothing
    // about this one, and a stale mark would make the backfill below fetch
    // a bogus range.
    highWaterMark.current = null

    async function loadHistory(room: string) {
      // Newest-first from the API; reverse for oldest-at-top display.
      const { data, error } = await apiClient.GET('/api/v1/rooms/{id}/messages', {
        params: { path: { id: room }, query: { limit: 50 } },
      })
      if (cancelled) return
      if (error || !data) {
        setLastError('Failed to load message history')
        return
      }
      const list = (data as unknown as ChatMessage[]) ?? []
      setMessages([...list].reverse())
      // Newest-first response: the first element is the room's high-water
      // mark. The max-guard keeps any live message that raced the fetch
      // ahead of it.
      const newest = list[0]
      if (newest) advanceHighWaterMark(newest.created_at)
    }

    if (roomId) {
      void loadHistory(roomId)
      // Reset ephemeral state for the new room.
      setTypingUsers([])
      setReadReceipts([])
      // Entering a room extinguishes its unread badge immediately; the
      // server-side cursor follows via markRead once the socket is open.
      useUnread.getState().clear(roomId)
    } else {
      setMessages([])
    }

    return () => {
      cancelled = true
    }
  }, [roomId, advanceHighWaterMark])

  // Backfill messages missed while the socket was down: live broadcast is
  // the only delivery path for new messages, so everything sent between a
  // drop and the reconnect is simply absent from local state. Runs on every
  // transition to 'open' with a known mark — the first connect after the
  // history load and each reconnect after an outage.
  useEffect(() => {
    if (!roomId || status !== 'open') return
    // No mark yet: the history load is still in flight and its snapshot
    // will cover the gap; the next status change re-triggers this effect.
    if (highWaterMark.current === null) return

    let cancelled = false

    async function backfill(room: string, after: string) {
      let cursor = after
      for (;;) {
        // Bound every page request: a hung response must not stall this
        // loop — and, through the queue below, every backfill after it.
        const controller = new AbortController()
        const timer = setTimeout(() => controller.abort(), BACKFILL_FETCH_TIMEOUT_MS)
        try {
          const { data, error } = await apiClient.GET('/api/v1/rooms/{id}/messages', {
            params: { path: { id: room }, query: { after: cursor, limit: 100 } },
            signal: controller.signal,
          })
          if (cancelled) return
          if (error || !data) {
            // Non-fatal: the live stream keeps working; the next reconnect
            // retries from the same mark.
            setLastError('Failed to backfill missed messages')
            return
          }
          const page = (data as unknown as ChatMessage[]) ?? []
          if (page.length === 0) return
          // Ascending response: merge by id so copies already delivered
          // live (or by an overlapping page) don't duplicate, then sort by
          // created_at. Sorting matters because a live message that landed
          // while the page was in flight sits at the list tail and is NEWER
          // than the whole page — a plain append would leave older messages
          // below it for good (MessageList renders the array as-is, no
          // sorting of its own). The sort is stable, so equal-millisecond
          // messages keep their relative order, and instants are compared
          // rather than strings (see the note at advanceHighWaterMark).
          setMessages((prev) => {
            const known = new Set(prev.map((m) => m.id))
            const fresh = page.filter((m) => !known.has(m.id))
            if (fresh.length === 0) return prev
            return [...prev, ...fresh].sort(
              (a, b) => Date.parse(a.created_at) - Date.parse(b.created_at),
            )
          })
          const last = page[page.length - 1]
          if (!last) return // unreachable: page is non-empty; satisfies indexing
          advanceHighWaterMark(last.created_at)
          // A full page may have more behind it; a short one is the tail.
          if (page.length < 100) return
          cursor = last.created_at
        } catch {
          // A timed-out or failed request rejects the fetch (openapi-fetch
          // does not catch it for us) — non-fatal like the HTTP error
          // above. A cleanup abort also lands here, but then `cancelled`
          // is already set, so no error is shown for a routine room switch.
          if (!cancelled) setLastError('Failed to backfill missed messages')
          return
        } finally {
          clearTimeout(timer)
        }
      }
    }

    // Serialize with any loop still awaiting a response: instead of racing
    // it (or dropping this trigger and losing the newer gap), wait for the
    // turn and walk the range from the CURRENT mark. The mark only ever
    // advances over messages already merged into local state, so a later
    // start cursor covers everything both runs missed.
    const queued = backfillQueue.current.then(() => {
      if (cancelled) return
      const after = highWaterMark.current
      if (after === null) return
      return backfill(roomId, after)
    })
    backfillQueue.current = queued.catch(() => {
      // Not expected — backfill swallows its own errors — but a rejecting
      // tail would silently kill every future backfill in the queue.
    })

    // Room switch mid-flight: whatever the loop fetches belongs to the old
    // room and must never be merged into the new room's list.
    return () => {
      cancelled = true
    }
  }, [roomId, status, advanceHighWaterMark])

  const send = useCallback((content: string): boolean => {
    const room = activeRoomRef.current
    if (!room) return false
    return getSocket()?.sendMessage(room, content) ?? false
  }, [])

  const edit = useCallback((messageId: string, content: string): boolean => {
    const room = activeRoomRef.current
    if (!room) return false
    return getSocket()?.sendEdit(room, messageId, content) ?? false
  }, [])

  // Toggle an emoji reaction; the broadcast "reaction" event updates the list.
  const react = useCallback((messageId: string, emoji: string): boolean => {
    const room = activeRoomRef.current
    if (!room) return false
    return getSocket()?.sendReaction(room, messageId, emoji) ?? false
  }, [])

  const sendTyping = useCallback(
    (isTyping: boolean) => {
      const room = activeRoomRef.current
      if (!room) return
      getSocket()?.sendTyping(room, isTyping)
    },
    [],
  )

  const markRead = useCallback(() => {
    const room = activeRoomRef.current
    if (!room) return
    getSocket()?.sendRead(room)
    // Zero the local badge right away; the server read cursor advances with
    // the "read" frame above.
    useUnread.getState().clear(room)
  }, [])

  return {
    messages,
    status,
    send,
    edit,
    react,
    sendTyping,
    markRead,
    lastError,
    typingUsers,
    onlineUsers,
    readReceipts,
  }
}
