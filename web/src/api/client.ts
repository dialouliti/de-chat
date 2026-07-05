import createClient from 'openapi-fetch'

import type { paths } from './schema'
import { getToken } from '@/store/auth'

// The API base URL. In dev, Vite proxies /api → localhost:8080 so this stays
// empty (same-origin). In production, set VITE_API_URL to the backend origin.
const baseUrl = import.meta.env.VITE_API_URL?.replace(/\/$/, '') ?? ''

export const apiClient = createClient<paths>({
  baseUrl,
})

// Inject the JWT on every request via middleware. openapi-fetch exposes a
// onRequest hook that runs before fetch; we pull the token from the auth
// store's localStorage mirror so this works even before React hydrates.
apiClient.use({
  async onRequest({ request }) {
    const token = getToken()
    if (token) {
      request.headers.set('Authorization', `Bearer ${token}`)
    }
    return request
  },
})

// Re-export the schema types for convenience.
export type { paths, components } from './schema'
