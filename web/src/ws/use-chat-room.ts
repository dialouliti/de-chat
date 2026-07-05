// useChatRoom ties together the REST message history and the live WebSocket
// stream for a single room.
//
// The server broadcasts every message to every connected client (see
// internal/hub/hub.go) — there is no server-side per-room subscription in v1.
// So the client filters incoming WS messages by the active room_id locally.
// This keeps the WS simple and means switching rooms is instant (no
// re-handshake), at the cost of some redundant traffic the browser discards.

import { useEffect, useRef, useState } from 'react'

import { apiClient } from '@/api/client'
import { ChatSocket, type ServerMessage } from './socket'

export type ChatMessage = {
  id: string
  room_id: string
  user_id: string
  username: string
  content: string
  created_at: string
}

export type ConnectionStatus = 'connecting' | 'open' | 'closed'

export interface UseChatRoomResult {
  messages: ChatMessage[]
  status: ConnectionStatus
  send: (content: string) => boolean
  /** Local-only error surfaced by the server (e.g. empty content). */
  lastError: string | null
}

export function useChatRoom(roomId: string | null): UseChatRoomResult {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [status, setStatus] = useState<ConnectionStatus>('closed')
  const [lastError, setLastError] = useState<string | null>(null)
  const socketRef = useRef<ChatSocket | null>(null)
  const activeRoomRef = useRef<string | null>(roomId)

  // Keep the ref current so the WS callback always sees the latest room.
  useEffect(() => {
    activeRoomRef.current = roomId
  }, [roomId])

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
    } else {
      setMessages([])
    }

    // (Re)create the socket once per mount.
    if (!socketRef.current) {
      const socket = new ChatSocket({
        onMessage: (m: ServerMessage) => handleServerMessage(m),
        onStatus: (s) => setStatus(s),
      })
      socketRef.current = socket
      socket.connect()
    }

    function handleServerMessage(m: ServerMessage) {
      if (m.type === 'pong') return
      if (m.type === 'error') {
        setLastError(m.text ?? 'unknown error')
        return
      }
      if (m.type === 'message' && m.message) {
        // Filter to the active room — the server sends every room.
        if (activeRoomRef.current && m.message.room_id === activeRoomRef.current) {
          setMessages((prev) => {
            // De-dup: the server may re-deliver, and our own optimistic
            // message will be replaced when the broadcast arrives.
            if (prev.some((p) => p.id === m.message!.id)) return prev
            return [...prev, m.message as ChatMessage]
          })
        }
      }
    }

    return () => {
      cancelled = true
    }
  }, [roomId])

  // Tear down the socket only when the component unmounts.
  useEffect(() => {
    const socket = socketRef.current
    return () => {
      socket?.close()
      socketRef.current = null
    }
  }, [])

  const send = (content: string): boolean => {
    const room = activeRoomRef.current
    if (!room) return false
    return socketRef.current?.send({ type: 'message', room_id: room, content }) ?? false
  }

  return { messages, status, send, lastError }
}
