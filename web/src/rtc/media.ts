// Microphone capture helpers for voice calls.
//
// UI-agnostic layer: openMic maps browser failures to short human-readable
// English messages (the app's UI language); how to surface them is up to
// the caller.

/** Opens the default microphone tuned for voice chat (AEC + NS + AGC). */
export async function openMic(): Promise<MediaStream> {
  if (!navigator.mediaDevices?.getUserMedia) {
    // getUserMedia is unavailable in insecure contexts and old browsers.
    throw new Error('Microphone is not available (a secure HTTPS context is required)')
  }
  try {
    return await navigator.mediaDevices.getUserMedia({
      audio: {
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true,
      },
    })
  } catch (err) {
    throw new Error(micErrorMessage(err))
  }
}

/** Stops every track of the stream, releasing the microphone. */
export function stopStream(stream: MediaStream): void {
  for (const track of stream.getTracks()) {
    track.stop()
  }
}

/** Map a getUserMedia failure to a user-facing English message. */
function micErrorMessage(err: unknown): string {
  // Legacy Firefox rejects with plain Errors whose name carries the reason.
  const name = err instanceof Error ? err.name : ''
  switch (name) {
    case 'NotAllowedError':
    case 'SecurityError':
      return 'Microphone access denied'
    case 'NotFoundError':
    case 'DevicesNotFoundError':
      return 'No microphone found'
    case 'NotReadableError':
    case 'TrackStartError':
      return 'Microphone is busy or unavailable'
    case 'OverconstrainedError':
      return 'Microphone does not meet the call requirements'
    case 'AbortError':
      return 'Microphone access was interrupted, please try again'
    default: {
      const detail = err instanceof Error && err.message ? ` (${err.message})` : ''
      return `Could not access microphone${detail}`
    }
  }
}
