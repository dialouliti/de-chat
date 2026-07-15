import { useEffect, useRef, useState } from 'react'

import { formatTime } from '@/lib/format'
import type { ChatMessage, ReadReceipt } from '@/ws/use-chat-room'

interface Props {
  messages: ChatMessage[]
  currentUsername: string | null
  readReceipts?: ReadReceipt[]
  onEdit?: (messageId: string, content: string) => void
}

/** Scrollable list of messages, auto-scrolling to the newest. Own messages
 * show a read-receipt count and an inline edit affordance. */
export function MessageList({ messages, currentUsername, readReceipts, onEdit }: Props) {
  const bottomRef = useRef<HTMLDivElement>(null)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [draft, setDraft] = useState('')

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  if (messages.length === 0) {
    return (
      <div className="flex h-full items-center justify-center text-slate-500">
        No messages yet — say hello.
      </div>
    )
  }

  function startEdit(m: ChatMessage) {
    setEditingId(m.id)
    setDraft(m.content)
  }

  function commitEdit() {
    if (editingId && onEdit && draft.trim()) {
      onEdit(editingId, draft.trim())
    }
    setEditingId(null)
    setDraft('')
  }

  return (
    <div className="scrollbar-thin h-full overflow-y-auto px-4 py-3">
      <ul className="space-y-1.5">
        {messages.map((m) => {
          const mine = m.username === currentUsername
          const editing = editingId === m.id

          // For my own messages, count how many receipts are at/after this
          // message's created_at (excluding myself).
          let readBy = 0
          if (mine && readReceipts) {
            const msgTime = new Date(m.created_at).getTime()
            readBy = readReceipts.filter(
              (r) => r.username !== currentUsername && new Date(r.last_read_at).getTime() >= msgTime,
            ).length
          }

          return (
            <li
              key={m.id}
              className={`group flex flex-col ${mine ? 'items-end' : 'items-start'}`}
            >
              <div className="mb-0.5 flex items-center gap-2 px-1 text-xs text-slate-500">
                <span className="font-medium text-slate-400">
                  {mine ? 'You' : m.username}
                </span>
                <span>{formatTime(m.created_at)}</span>
                {m.edited_at && <span className="italic">(edited)</span>}
              </div>

              {editing ? (
                <div className="flex max-w-[75%] items-end gap-1">
                  <textarea
                    value={draft}
                    onChange={(e) => setDraft(e.target.value)}
                    autoFocus
                    rows={1}
                    className="resize-none rounded-lg border border-brand bg-slate-950 px-3 py-1.5 text-sm text-slate-100 focus:outline-none"
                  />
                  <button
                    onClick={commitEdit}
                    className="rounded bg-brand px-2 py-1 text-xs text-white hover:bg-brand-dark"
                  >
                    Save
                  </button>
                  <button
                    onClick={() => setEditingId(null)}
                    className="rounded bg-slate-700 px-2 py-1 text-xs text-slate-200 hover:bg-slate-600"
                  >
                    Cancel
                  </button>
                </div>
              ) : (
                <div
                  className={`max-w-[75%] whitespace-pre-wrap break-words rounded-lg px-3 py-1.5 text-sm ${
                    mine ? 'bg-brand text-white' : 'bg-slate-800 text-slate-100'
                  }`}
                >
                  {m.content}
                </div>
              )}

              {/* Footer row: read receipt + edit affordance (own messages only). */}
              {!editing && mine && (
                <div className="mt-0.5 flex items-center gap-2 px-1 text-xs text-slate-500">
                  {readBy > 0 && <span>Read by {readBy}</span>}
                  {onEdit && (
                    <button
                      onClick={() => startEdit(m)}
                      className="opacity-0 transition group-hover:opacity-100 hover:text-slate-300"
                      title="Edit message"
                    >
                      edit
                    </button>
                  )}
                </div>
              )}
            </li>
          )
        })}
      </ul>
      <div ref={bottomRef} />
    </div>
  )
}
