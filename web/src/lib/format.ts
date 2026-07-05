// Small formatting helpers shared across UI components.

/** Format a timestamp as a short local time, e.g. "14:05" or "Jul 4, 14:05". */
export function formatTime(ts: string): string {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ''
  const now = new Date()
  const sameDay = d.toDateString() === now.toDateString()
  return sameDay
    ? d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    : d.toLocaleDateString([], { month: 'short', day: 'numeric' }) +
        ' ' +
        d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

/** Take the first 8 chars of a uuid for compact display. */
export function shortId(id: string): string {
  return id.slice(0, 8)
}

/**
 * Convert an http(s) base URL into a ws(s) URL. Used for the WebSocket
 * endpoint, since the API URL is configured as http.
 */
export function toWsUrl(httpBase: string, path: string): string {
  const u = new URL(httpBase)
  u.protocol = u.protocol === 'https:' ? 'wss:' : 'ws:'
  u.pathname = path
  return u.toString()
}
