import { useEffect, useRef, useState, type KeyboardEvent } from 'react'

interface Props {
  onSend: (content: string) => void
  onTyping?: (isTyping: boolean) => void
  disabled?: boolean
}

/** Single-line input that sends on Enter (Shift+Enter inserts a newline).
 * Emits typing events as the user types (debounced + start/stop semantics). */
export function MessageInput({ onSend, onTyping, disabled }: Props) {
  const [value, setValue] = useState('')
  const isTypingRef = useRef(false)
  const stopTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Clear typing state when the composer is disabled (e.g. disconnected).
  useEffect(() => {
    if (disabled && isTypingRef.current) {
      isTypingRef.current = false
      onTyping?.(false)
    }
  }, [disabled, onTyping])

  // Stop typing shortly after the user goes idle, so a "stopped" signal fires
  // even if they never press Enter (e.g. they tab away).
  function flagTyping() {
    if (stopTimer.current) clearTimeout(stopTimer.current)
    if (!isTypingRef.current) {
      isTypingRef.current = true
      onTyping?.(true)
    }
    stopTimer.current = setTimeout(() => {
      if (isTypingRef.current) {
        isTypingRef.current = false
        onTyping?.(false)
      }
    }, 2500)
  }

  function submit() {
    const trimmed = value.trim()
    if (!trimmed) return
    onSend(trimmed)
    setValue('')
    if (isTypingRef.current) {
      isTypingRef.current = false
      onTyping?.(false)
    }
    if (stopTimer.current) clearTimeout(stopTimer.current)
  }

  function handleKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      submit()
    }
  }

  return (
    <div className="border-t border-slate-800 bg-slate-900 p-3">
      <div className="flex items-end gap-2">
        <textarea
          value={value}
          onChange={(e) => {
            setValue(e.target.value)
            if (e.target.value.trim()) flagTyping()
          }}
          onKeyDown={handleKeyDown}
          rows={1}
          disabled={disabled}
          placeholder="Type a message…  (Enter to send, Shift+Enter for newline)"
          className="flex-1 resize-none rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 placeholder:text-slate-500 focus:border-brand focus:outline-none disabled:opacity-50"
        />
        <button
          onClick={submit}
          disabled={disabled || !value.trim()}
          className="rounded-lg bg-brand px-4 py-2 text-sm font-medium text-white hover:bg-brand-dark disabled:opacity-50"
        >
          Send
        </button>
      </div>
    </div>
  )
}
