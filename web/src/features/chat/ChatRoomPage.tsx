import { useEffect } from 'react'
import { Link, useParams } from 'react-router-dom'

import { Layout } from '@/components/Layout'
import { MessageList } from '@/components/MessageList'
import { MessageInput } from '@/components/MessageInput'
import { useAuth } from '@/store/auth'
import { requestNotificationPermission } from '@/lib/notifications'
import { useChatRoom } from '@/ws/use-chat-room'

export function ChatRoomPage() {
  const { id } = useParams<{ id: string }>()
  const username = useAuth((s) => s.username)
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
            <span className="ml-auto text-xs text-red-400">{lastError}</span>
          )}
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
