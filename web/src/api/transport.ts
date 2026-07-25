// The single Connect transport instance for the app. connect-query keys its
// cache by transport identity, so exactly one instance may exist anywhere in
// the codebase — a second instance would silently split the cache.
import { createConnectTransport } from "@connectrpc/connect-web"

export const transport = createConnectTransport({ baseUrl: "/rpc" })
