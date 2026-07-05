/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Base URL of the deChat backend (proxied in dev). */
  readonly VITE_API_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
