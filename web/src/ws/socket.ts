// A small WebSocket wrapper tailored to the deChat server protocol.
//
// Server behavior (see internal/ws/handler.go):
//   - Pings every ~54s; drops the connection if no pong within 60s.
//   - Sends ServerMsg envelopes: {type, message?, ...}
//   - Broadcasts to ALL connected clients; the client filters by room_id.
//   - Presence events are global (no room_id); handled specially by consumers.
//
// This wrapper handles: connect with ?token=, exponential-backoff reconnect,
// ping heartbeat, and typed dispatch of incoming ServerMsgs.

import { getToken } from '@/store/auth'
import { toWsUrl } from '@/lib/format'

export interface MessagePayload {
  id: string
  room_id: string
  user_id: string
  username: string
  content: string
  created_at: string
  edited_at?: string | null
}

export interface PresenceUser {
  user_id: string
  username: string
}

export interface ReadReceiptPayload {
  user_id: string
  username: string
  last_read_at: string
}

export type ServerMessage = {
  type: 'message' | 'pong' | 'error'
  message?: MessagePayload
  message_id?: string
  text?: string
} | {
  type: 'typing'
  room_id: string
  user_id: string
  username: string
  is_typing: boolean
} | {
  type: 'presence'
  user_id: string
  username: string
  is_online: boolean
  online_users?: PresenceUser[]
} | {
  type: 'read_receipt'
  room_id: string
  read_by: ReadReceiptPayload[]
} | {
  type: 'message_edited'
  message: MessagePayload
  message_id: string
}

export type ServerMessageHandler = (m: ServerMessage) => void

const API_BASE =
  import.meta.env.VITE_API_URL?.replace(/\/$/, '') ?? window.location.origin

// Match the server's ping period (54s) — keeping the client's heartbeat
// aligned means we send our own ping slightly more often than the server's
// timeout window, so a stalled connection is detected promptly.
const PING_INTERVAL_MS = 30_000
const MAX_BACKOFF_MS = 15_000

export class ChatSocket {
  private ws: WebSocket | null = null
  private token: string
  private onMessage: ServerMessageHandler
  private onStatus?: (status: 'connecting' | 'open' | 'closed') => void

  private pingTimer: ReturnType<typeof setInterval> | null = null
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private backoff = 1_000
  private manuallyClosed = false

  constructor(opts: {
    onMessage: ServerMessageHandler
    onStatus?: (status: 'connecting' | 'open' | 'closed') => void
  }) {
    this.onMessage = opts.onMessage
    this.onStatus = opts.onStatus
    this.token = getToken() ?? ''
  }

  connect() {
    this.manuallyClosed = false
    this.open()
  }

  private open() {
    if (!this.token) {
      // No token — nothing to connect with. The caller should log in first.
      return
    }
    this.onStatus?.('connecting')

    const url = toWsUrl(API_BASE, '/api/v1/ws') + `?token=${encodeURIComponent(this.token)}`
    const ws = new WebSocket(url)
    this.ws = ws

    ws.onopen = () => {
      this.backoff = 1_000
      this.onStatus?.('open')
      this.startPing()
    }

    ws.onmessage = (ev) => {
      let parsed: ServerMessage
      try {
        parsed = JSON.parse(ev.data) as ServerMessage
      } catch {
        return
      }
      this.onMessage(parsed)
    }

    ws.onclose = () => {
      this.stopPing()
      this.ws = null
      this.onStatus?.('closed')
      if (!this.manuallyClosed) {
        this.scheduleReconnect()
      }
    }

    // Browser WS fires onerror then onclose; we do all cleanup in onclose.
    ws.onerror = () => {
      // no-op; onclose will handle reconnect
    }
  }

  send(raw: object): boolean {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false
    this.ws.send(JSON.stringify(raw))
    return true
  }

  /** Send a chat message. */
  sendMessage(roomId: string, content: string): boolean {
    return this.send({ type: 'message', room_id: roomId, content })
  }

  /** Notify the room that the user started or stopped typing. */
  sendTyping(roomId: string, isTyping: boolean): boolean {
    return this.send({ type: 'typing', room_id: roomId, is_typing: isTyping })
  }

  /** Mark a room as read (advances the read cursor). */
  sendRead(roomId: string): boolean {
    return this.send({ type: 'read', room_id: roomId })
  }

  /** Edit one of the caller's own messages. */
  sendEdit(roomId: string, messageId: string, content: string): boolean {
    return this.send({ type: 'edit', room_id: roomId, message_id: messageId, content })
  }

  close() {
    this.manuallyClosed = true
    this.stopPing()
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    this.ws?.close()
    this.ws = null
  }

  private startPing() {
    this.stopPing()
    this.pingTimer = setInterval(() => {
      this.send({ type: 'ping' })
    }, PING_INTERVAL_MS)
  }

  private stopPing() {
    if (this.pingTimer) {
      clearInterval(this.pingTimer)
      this.pingTimer = null
    }
  }

  private scheduleReconnect() {
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    const delay = Math.min(this.backoff, MAX_BACKOFF_MS)
    this.backoff = Math.min(this.backoff * 2, MAX_BACKOFF_MS)
    this.reconnectTimer = setTimeout(() => this.open(), delay)
  }
}
