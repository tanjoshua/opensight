import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { TransportProvider } from "@connectrpc/connect-query"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { BrowserRouter } from "react-router"

import "./index.css"
import App from "./App.tsx"
import { transport } from "@/api/transport"
import { ThemeProvider } from "@/components/theme-provider.tsx"

const queryClient = new QueryClient()

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <TransportProvider transport={transport}>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <ThemeProvider>
            <App />
          </ThemeProvider>
        </BrowserRouter>
      </QueryClientProvider>
    </TransportProvider>
  </StrictMode>
)
