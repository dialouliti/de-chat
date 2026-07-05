import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { VitePWA } from 'vite-plugin-pwa'
import path from 'node:path'

// The dev server proxies API + spec requests to the Go backend so the client
// and server can run side-by-side without CORS gymnastics during development.
// In production the same origin (or a configured VITE_API_URL) serves both.
export default defineConfig({
  plugins: [
    react(),
    VitePWA({
      registerType: 'autoUpdate',
      includeAssets: ['icons/*.png'],
      manifest: {
        name: 'deChat',
        short_name: 'deChat',
        description: 'Private group chat',
        theme_color: '#0f172a',
        background_color: '#0f172a',
        display: 'standalone',
        start_url: '/',
        icons: [
          { src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png' },
          { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png' },
          { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
        ],
      },
      // For a private app with no critical offline content, dev-friendly defaults
      // are fine. The service worker caches the app shell; messages are always
      // fetched live.
      workbox: {
        globPatterns: ['**/*.{js,css,html,ico,png,svg,woff2}'],
        // Never cache the API or the WebSocket.
        navigateFallbackDenylist: [/^\/api/, /^\/openapi/, /^\/healthz/],
      },
    }),
  ],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src'),
    },
  },
  server: {
    host: true, // bind to 0.0.0.0 so the dev server is reachable on the LAN
    port: 5173,
    proxy: {
      // REST API
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
      // WebSocket — needs ws:true so the upgrade is forwarded
      '/api/v1/ws': {
        target: 'ws://localhost:8080',
        ws: true,
        changeOrigin: true,
      },
      // Spec + health (handy in dev)
      '/openapi.json': 'http://localhost:8080',
      '/openapi.yaml': 'http://localhost:8080',
      '/healthz': 'http://localhost:8080',
    },
  },
})
