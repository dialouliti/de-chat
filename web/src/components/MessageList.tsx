import { useEffect, useRef, useState } from 'react'

import { formatTime } from '@/lib/format'
import type { ReactionEntry } from '@/ws/socket'
import type { ChatMessage, ReadReceipt } from '@/ws/use-chat-room'

/** Quick-reaction palette offered on every message. */
const QUICK_EMOJIS = ['👍', '❤️', '😂', '😮', '😢', '🎉'] as const

type ReactionGroup = { emoji: string; entries: ReactionEntry[] }

/** Group a message's reactions by emoji, keeping first-appearance order
 * (the server aggregates them oldest-reaction-first). */
function groupReactions(reactions: ReactionEntry[] | undefined): ReactionGroup[] {
  const byEmoji = new Map<string, ReactionEntry[]>()
  for (const r of reactions ?? []) {
    const list = byEmoji.get(r.emoji)
    if (list) list.push(r)
    else byEmoji.set(r.emoji, [r])
  }
  return [...byEmoji.entries()].map(([emoji, entries]) => ({ emoji, entries }))
}

interface Props {
  messages: ChatMessage[]
  currentUsername: string | null
  readReceipts?: ReadReceipt[]
  onEdit?: (messageId: string, content: string) => void
  onReact?: (messageId: string, emoji: string) => void
}

/** Scrollable list of messages, auto-scrolling to the newest. Own messages
 * show a read-receipt count and an inline edit affordance. Every message
 * shows reaction chips (click to toggle) and a quick-reaction picker. */
export function MessageList({ messages, currentUsername, readReceipts, onEdit, onReact }: Props) {
  const bottomRef = useRef<HTMLDivElement>(null)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [draft, setDraft] = useState('')
  // Which message's quick-reaction palette is open (one at a time).
  const [pickerId, setPickerId] = useState<string | null>(null)

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
          const reactionGroups = groupReactions(m.reactions)

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
                {m.edited_at && (
                  <time
                    dateTime={m.edited_at}
                    title={`edited at ${formatTime(m.edited_at)}`}
                    className="italic"
                  >
                    (edited)
                  </time>
                )}
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

              {/* Footer row: reaction chips + quick-react picker for every
                  message; read receipt + edit affordance for own messages
                  (edit only while it hasn't been edited — one edit per message).
                  The quick-react button stays visible without hover so it works
                  on touch devices. */}
              {!editing && (reactionGroups.length > 0 || onReact) && (
                <div className="mt-0.5 flex flex-wrap items-center gap-1 px-1 text-xs text-slate-500">
                  {reactionGroups.map(({ emoji, entries }) => {
                    const mineReaction = entries.some((r) => r.username === currentUsername)
                    return (
                      <button
                        key={emoji}
                        onClick={() => onReact?.(m.id, emoji)}
                        disabled={!onReact}
                        title={entries.map((e) => e.username).join(', ')}
                        className={`flex items-center gap-1 rounded-full border px-2 py-0.5 leading-none transition ${
                          mineReaction
                            ? 'border-brand bg-brand/20 text-white'
                            : 'border-slate-700 bg-slate-800/60 text-slate-300 hover:border-slate-500 hover:text-slate-100'
                        } ${onReact ? '' : 'cursor-default'}`}
                      >
                        <span aria-hidden>{emoji}</span>
                        <span>{entries.length}</span>
                      </button>
                    )
                  })}

                  {onReact && (
                    <span className="flex items-center">
                      <button
                        onClick={() => setPickerId(pickerId === m.id ? null : m.id)}
                        className="rounded-full px-1.5 py-0.5 text-slate-500 transition hover:bg-slate-800 hover:text-slate-200"
                        title="Add reaction"
                        aria-label="Add reaction"
                        aria-expanded={pickerId === m.id}
                      >
                        🙂+
                      </button>
                      {pickerId === m.id && (
                        <span className="flex items-center gap-0.5 rounded-full border border-slate-700 bg-slate-900 px-1.5 py-0.5">
                          {QUICK_EMOJIS.map((emoji) => (
                            <button
                              key={emoji}
                              onClick={() => {
                                onReact(m.id, emoji)
                                setPickerId(null)
                              }}
                              className="rounded px-1 py-0.5 transition hover:bg-slate-700"
                              aria-label={`React with ${emoji}`}
                            >
                              {emoji}
                            </button>
                          ))}
                        </span>
                      )}
                    </span>
                  )}

                  {mine && readBy > 0 && <span className="ml-1">Read by {readBy}</span>}
                  {mine && !m.edited_at && onEdit && (
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
