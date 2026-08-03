import path from "path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    // In dev the Go API runs separately (HTTP_ADDR=:8080); in production the
    // SPA is served by the same binary, so no CORS anywhere (design 06).
    proxy: {
      "/rpc": process.env.OPENSIGHT_API_URL ?? "http://127.0.0.1:8080",
      // Google's redirect back to /auth/google/callback lands on the Vite
      // origin (APP_BASE_URL=http://localhost:5173 in dev); proxy it to the
      // Go server that actually handles the exchange.
      "/auth": process.env.OPENSIGHT_API_URL ?? "http://127.0.0.1:8080",
    },
  },
})
