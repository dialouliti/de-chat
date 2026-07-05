import { Link, useParams } from 'react-router-dom'

import { Layout } from '@/components/Layout'
import { MessageList } from '@/components/MessageList'
import { MessageInput } from '@/components/MessageInput'
import { useAuth } from '@/store/auth'
import { useChatRoom } from '@/ws/use-chat-room'

export function ChatRoomPage() {
  const { id } = useParams<{ id: string }>()
  const username = useAuth((s) => s.username)
  const { messages, status, send, lastError } = useChatRoom(id ?? null)

  // Status dot — green when connected, amber when connecting, red when closed.
  const dot =
    status === 'open'
      ? 'bg-green-500'
      : status === 'connecting'
        ? 'bg-amber-500'
        : 'bg-red-500'

  return (
    <Layout>
      <div className="flex h-full flex-col">
        {/* Room header */}
        <div className="flex items-center gap-3 border-b border-slate-800 bg-slate-900 px-4 py-2">
          <Link
            to="/rooms"
            className="rounded px-2 py-1 text-sm text-slate-400 hover:bg-slate-800 hover:text-slate-200"
          >
            ← Rooms
          </Link>
          <div className="flex items-center gap-2">
            <span className={`h-2 w-2 rounded-full ${dot}`} title={status} />
            <span className="text-sm text-slate-400">
              {status === 'open' ? 'connected' : status}
            </span>
          </div>
          {lastError && (
            <span className="ml-auto text-xs text-red-400">{lastError}</span>
          )}
        </div>

        {/* Messages */}
        <div className="flex-1 overflow-hidden">
          <MessageList messages={messages} currentUsername={username} />
        </div>

        {/* Composer */}
        <MessageInput onSend={(content) => send(content)} disabled={status !== 'open'} />
      </div>
    </Layout>
  )
}
