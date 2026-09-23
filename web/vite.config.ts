import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The dev server proxies the gateway's paths so the console talks to its own
// origin, exactly as it does behind Caddy in production. Without this, local
// development would be the one place the session cookie crosses origins —
// and the one place that behaves differently from what ships.
const gateway = process.env.MUTEGATE_DEV_GATEWAY ?? process.env.APERTURE_DEV_GATEWAY ?? 'http://localhost:8080'
const proxied = ['/api', '/admin', '/v1', '/health', '/ready']

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: Object.fromEntries(proxied.map((p) => [p, { target: gateway, changeOrigin: false }])),
  },
})
