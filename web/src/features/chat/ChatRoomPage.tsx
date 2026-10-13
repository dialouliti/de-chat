import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'

import { apiClient } from '@/api/client'
import { Layout } from '@/components/Layout'
import { MessageList } from '@/components/MessageList'
import { MessageInput } from '@/components/MessageInput'
import { useAuth } from '@/store/auth'
import { callInProgress, useCall } from '@/store/call'
import { requestNotificationPermission } from '@/lib/notifications'
import { startCall } from '@/features/chat/call-signaling'
import { useChatRoom } from '@/ws/use-chat-room'

/** One entry of GET /api/v1/rooms/{id}/members (models.RoomMember). */
interface RoomMember {
  room_id: string
  user_id: string
  username: string
  role: string
  joined_at: string
}

export function ChatRoomPage() {
  const { id } = useParams<{ id: string }>()
  const username = useAuth((s) => s.username)
  const [callMenuOpen, setCallMenuOpen] = useState(false)
  const {
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
  } = useChatRoom(id ?? null)

  // Room members for the call picker. Voice calls are 1:1, so the Call
  // button lists the room's other members (self excluded by username —
  // usernames are unique) and the pick drives startCall. Members with no
  // live presence entry are shown greyed as offline; calling them yields
  // "User is not online" from the server.
  const callBusy = useCall(callInProgress)
  const { data: members } = useQuery({
    queryKey: ['room-members', id],
    queryFn: async () => {
      const { data, error } = await apiClient.GET('/api/v1/rooms/{id}/members', {
        params: { path: { id: id! } },
      })
      if (error || !data) throw new Error('failed to load members')
      return (data as unknown as RoomMember[]) ?? []
    },
    enabled: !!id,
  })
  const onlineIds = new Set(onlineUsers.map((u) => u.user_id))
  const otherMembers = (members ?? []).filter((m) => m.username !== username)

  // Close the picker when switching rooms; stale menus point at the wrong room.
  useEffect(() => {
    setCallMenuOpen(false)
  }, [id])

  // Mark the room read whenever we enter it or new messages arrive. Skips
  // marking our own send (handled implicitly: the cursor advances to now).
  useEffect(() => {
    if (status === 'open') markRead()
  }, [id, status, messages.length, markRead])

  // Status dot — green when connected, amber when connecting, red when closed.
  const dot =
    status === 'open'
      ? 'bg-green-500'
      : status === 'connecting'
        ? 'bg-amber-500'
        : 'bg-red-500'

  // Typing indicator text: "Alice is typing…" / "Alice, Bob are typing…"
  let typingLabel = ''
  if (typingUsers.length === 1) typingLabel = `${typingUsers[0]} is typing…`
  else if (typingUsers.length === 2)
    typingLabel = `${typingUsers[0]} and ${typingUsers[1]} are typing…`
  else if (typingUsers.length > 2) typingLabel = `${typingUsers.length} people are typing…`

  return (
    <Layout>
      <div className="flex h-full flex-col">
        {/* Room header */}
        <div className="flex flex-wrap items-center gap-3 border-b border-slate-800 bg-slate-900 px-4 py-2">
          <Link
            to="/rooms"
            className="rounded px-2 py-1 text-sm text-slate-400 hover:bg-slate-800 hover:text-slate-200"
          >
            ← Rooms
          </Link>
          <Link
            to={`/rooms/${id}/members`}
            className="rounded px-2 py-1 text-sm text-slate-400 hover:bg-slate-800 hover:text-slate-200"
          >
            Members
          </Link>
          <div className="flex items-center gap-2">
            <span className={`h-2 w-2 rounded-full ${dot}`} title={status} />
            <span className="text-sm text-slate-400">
              {status === 'open' ? 'connected' : status}
            </span>
          </div>
          <span className="text-xs text-slate-500">
            {onlineUsers.length} online
          </span>
          {lastError && (
            <span className="ml-3 text-xs text-red-400">{lastError}</span>
          )}

          {/* Voice call picker: one call at a time over a live socket, so the
              button and every entry disable while a call is in flight or the
              connection is down (an offer sent mid-reconnect is dropped by
              send() and would leave an eternal "Calling…" card). */}
          <div className="relative ml-auto">
            <button
              onClick={() => setCallMenuOpen((open) => !open)}
              disabled={callBusy || status !== 'open'}
              title={status !== 'open' ? 'Reconnecting…' : undefined}
              className="rounded px-3 py-1.5 text-sm text-slate-400 hover:bg-slate-800 hover:text-slate-200 disabled:cursor-not-allowed disabled:opacity-50"
            >
              Call
            </button>
            {callMenuOpen && (
              <div className="absolute right-0 top-full z-40 mt-1 w-60 rounded-lg border border-slate-800 bg-slate-900 p-2 shadow-lg">
                <p className="px-2 py-1 text-xs text-slate-500">Start a call with…</p>
                {otherMembers.length === 0 && (
                  <p className="px-2 py-2 text-xs text-slate-500">
                    Nobody else is a member of this room yet.
                  </p>
                )}
                {otherMembers.map((m) => {
                  const online = onlineIds.has(m.user_id)
                  return (
                    <button
                      key={m.user_id}
                      onClick={() => {
                        setCallMenuOpen(false)
                        if (id) void startCall(id, { userId: m.user_id, username: m.username })
                      }}
                      disabled={callBusy || status !== 'open'}
                      className="flex w-full items-center justify-between rounded px-2 py-1.5 text-sm text-slate-200 hover:bg-slate-800 disabled:cursor-not-allowed disabled:opacity-50"
                    >
                      <span>{m.username}</span>
                      <span
                        className={`h-2 w-2 rounded-full ${
                          online ? 'bg-green-500' : 'bg-slate-600'
                        }`}
                        title={online ? 'online' : 'offline'}
                      />
                    </button>
                  )
                })}
              </div>
            )}
          </div>
        </div>

        {/* Messages */}
        <div className="flex-1 overflow-hidden">
          <MessageList
            messages={messages}
            currentUsername={username}
            readReceipts={readReceipts}
            onEdit={edit}
            onReact={react}
          />
        </div>

        {/* Typing indicator */}
        <div className="h-5 px-4 text-xs italic text-slate-500">
          {typingLabel || '\u00A0' /* reserve height to avoid layout shift */}
        </div>

        {/* Composer */}
        <MessageInput
          onSend={(content) => {
            // Notification permission needs a user gesture — ask once, on
            // the first message this session (no-op afterwards).
            requestNotificationPermission()
            send(content)
          }}
          onTyping={sendTyping}
          disabled={status !== 'open'}
        />
      </div>
    </Layout>
  )
}
