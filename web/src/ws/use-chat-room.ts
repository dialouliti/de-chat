// useChatRoom ties together the REST message history and the live WebSocket
// stream for a single room.
//
// The server broadcasts every message to every connected client (see
// internal/hub/hub.go) — there is no server-side per-room subscription in v1.
// So the client filters incoming WS messages by the active room_id locally.
// Presence events are global (no room_id) and handled separately.

import { useCallback, useEffect, useRef, useState } from 'react'

import { apiClient } from '@/api/client'
import { useAuth } from '@/store/auth'
import { useUnread } from '@/store/unread'
import { showNotification } from '@/lib/notifications'
import { ChatSocket, type ReactionEntry, type ServerMessage } from './socket'

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

export type ConnectionStatus = 'connecting' | 'open' | 'closed'

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

export function useChatRoom(roomId: string | null): UseChatRoomResult {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [status, setStatus] = useState<ConnectionStatus>('closed')
  const [lastError, setLastError] = useState<string | null>(null)
  const [typingUsers, setTypingUsers] = useState<string[]>([])
  const [onlineUsers, setOnlineUsers] = useState<{ user_id: string; username: string }[]>([])
  const [readReceipts, setReadReceipts] = useState<ReadReceipt[]>([])

  const socketRef = useRef<ChatSocket | null>(null)
  const activeRoomRef = useRef<string | null>(roomId)
  const typingTimeouts = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map())

  // Keep the ref current so the WS callback always sees the latest room.
  useEffect(() => {
    activeRoomRef.current = roomId
  }, [roomId])

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
          // Own messages never count as unread (matches the server, which
          // excludes the caller's messages from GET /me/unread). Usernames
          // are unique in deChat, so a username comparison identifies the
          // author reliably.
          const own = useAuth.getState().username === msg.username
          if (!own && msg.room_id !== activeRoomRef.current) {
            // Message for a room we're not looking at — bump its badge.
            useUnread.getState().increment(msg.room_id)
          }
          // Desktop notification for other people's messages; the tab is
          // most likely in the background, but showNotification re-checks
          // document.hidden itself.
          if (!own) showNotification(msg.username, msg.content)
          if (activeRoomRef.current && msg.room_id === activeRoomRef.current) {
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

        case 'presence': {
          if (m.online_users) {
            // Initial snapshot delivered to the newcomer.
            setOnlineUsers(m.online_users)
          }
          setOnlineUsers((prev) => {
            if (m.is_online) {
              if (prev.some((u) => u.user_id === m.user_id)) return prev
              return [...prev, { user_id: m.user_id, username: m.username }]
            }
            return prev.filter((u) => u.user_id !== m.user_id)
          })
          return
        }

        case 'read_receipt':
          if (activeRoomRef.current && m.room_id === activeRoomRef.current) {
            setReadReceipts(m.read_by)
          }
          return
      }
    },
    [scheduleTypingClear],
  )

  // Load history when entering a room, then connect (or just reconnect) the
  // socket. The socket is per-mount; switching rooms does NOT tear it down,
  // because the server pushes all rooms down one socket.
  useEffect(() => {
    let cancelled = false

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

    // (Re)create the socket once per mount.
    if (!socketRef.current) {
      const socket = new ChatSocket({
        onMessage: handleServerMessage,
        onStatus: (s) => setStatus(s),
      })
      socketRef.current = socket
      socket.connect()
    }

    return () => {
      cancelled = true
    }
  }, [roomId, handleServerMessage])

  // Tear down the socket only when the component unmounts.
  useEffect(() => {
    const socket = socketRef.current
    return () => {
      socket?.close()
      socketRef.current = null
    }
  }, [])

  const send = useCallback((content: string): boolean => {
    const room = activeRoomRef.current
    if (!room) return false
    return socketRef.current?.sendMessage(room, content) ?? false
  }, [])

  const edit = useCallback((messageId: string, content: string): boolean => {
    const room = activeRoomRef.current
    if (!room) return false
    return socketRef.current?.sendEdit(room, messageId, content) ?? false
  }, [])

  // Toggle an emoji reaction; the broadcast "reaction" event updates the list.
  const react = useCallback((messageId: string, emoji: string): boolean => {
    const room = activeRoomRef.current
    if (!room) return false
    return socketRef.current?.sendReaction(room, messageId, emoji) ?? false
  }, [])

  const sendTyping = useCallback(
    (isTyping: boolean) => {
      const room = activeRoomRef.current
      if (!room) return
      socketRef.current?.sendTyping(room, isTyping)
    },
    [],
  )

  const markRead = useCallback(() => {
    const room = activeRoomRef.current
    if (!room) return
    socketRef.current?.sendRead(room)
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
