import { useEffect, useRef } from 'react'

import { formatTime } from '@/lib/format'
import type { ChatMessage } from '@/ws/use-chat-room'

interface Props {
  messages: ChatMessage[]
  currentUsername: string | null
}

/** Scrollable list of messages, auto-scrolling to the newest. */
export function MessageList({ messages, currentUsername }: Props) {
  const bottomRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    // Scroll to bottom whenever a new message arrives.
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  if (messages.length === 0) {
    return (
      <div className="flex h-full items-center justify-center text-slate-500">
        No messages yet — say hello.
      </div>
    )
  }

  return (
    <div className="scrollbar-thin h-full overflow-y-auto px-4 py-3">
      <ul className="space-y-1.5">
        {messages.map((m) => {
          const mine = m.username === currentUsername
          return (
            <li
              key={m.id}
              className={`flex flex-col ${mine ? 'items-end' : 'items-start'}`}
            >
              <div className="mb-0.5 px-1 text-xs text-slate-500">
                <span className="font-medium text-slate-400">
                  {mine ? 'You' : m.username}
                </span>
                <span className="ml-2">{formatTime(m.created_at)}</span>
              </div>
              <div
                className={`max-w-[75%] whitespace-pre-wrap break-words rounded-lg px-3 py-1.5 text-sm ${
                  mine
                    ? 'bg-brand text-white'
                    : 'bg-slate-800 text-slate-100'
                }`}
              >
                {m.content}
              </div>
            </li>
          )
        })}
      </ul>
      <div ref={bottomRef} />
    </div>
  )
}
