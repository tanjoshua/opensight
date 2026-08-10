import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import {
  createConnectQueryKey,
  TransportProvider,
} from "@connectrpc/connect-query"
import {
  MutationCache,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query"
import { BrowserRouter } from "react-router"

import "./index.css"
import App from "./App.tsx"
import { accessDeniedFrom } from "@/api/errors"
import { transport } from "@/api/transport"
import { getMe } from "@/gen/opensight/v1/auth-AuthService_connectquery"

// A mutation rejected by the RPC access gate (BILL-6/BILL-10) means the SPA's
// cached access has gone stale — most commonly a subscription lapsing
// mid-session. Invalidating getMe here, in one place, means every consumer
// (the shell banner, the onboarding guard) picks up the drop on its own
// rather than the app silently disagreeing with the server until a reload.
const queryClient = new QueryClient({
  mutationCache: new MutationCache({
    onError: (error) => {
      if (accessDeniedFrom(error) !== undefined) {
        void queryClient.invalidateQueries({
          queryKey: createConnectQueryKey({
            schema: getMe,
            cardinality: "finite",
          }),
        })
      }
    },
  }),
})

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <TransportProvider transport={transport}>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </QueryClientProvider>
    </TransportProvider>
  </StrictMode>
)
