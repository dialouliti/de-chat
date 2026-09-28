// Web Notifications for messages that arrive while the tab is hidden.
//
// Permission is requested lazily — once, at the user's first outgoing message
// (a user gesture, which most browsers require for the prompt) — instead of
// on login, so we don't nag before the user has shown any intent to chat.

let permissionRequested = false

/**
 * Ask the browser for notification permission, at most once per page load.
 * Safe to call on every send: after the first call it's a no-op. Browsers
 * remember the decision, so 'granted'/'denied' are also skipped.
 */
export function requestNotificationPermission(): void {
  if (permissionRequested) return
  if (typeof Notification === 'undefined') return // unsupported (e.g. iOS Safari)
  if (Notification.permission !== 'default') {
    permissionRequested = true
    return
  }
  permissionRequested = true
  void Notification.requestPermission()
}

/**
 * Show a notification for an incoming message. Silently skipped when the
 * page is visible or permission wasn't granted — unread badges still work,
 * so failing quietly is fine. The caller is responsible for skipping the
 * current user's own messages.
 */
export function showNotification(title: string, body: string): void {
  if (typeof Notification === 'undefined') return
  if (!document.hidden) return
  if (Notification.permission !== 'granted') return
  try {
    new Notification(title, { body })
  } catch {
    // Some platforms require a service-worker registration to show
    // notifications; ignore — this is best-effort.
  }
}
