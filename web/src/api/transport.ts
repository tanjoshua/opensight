// The single Connect transport instance for the app. connect-query keys its
// cache by transport identity, so exactly one instance may exist anywhere in
// the codebase — a second instance would silently split the cache.
import type { Interceptor } from "@connectrpc/connect"
import { createConnectTransport } from "@connectrpc/connect-web"

import { accountSlugFromPathname } from "@/lib/account-path"

const accountInterceptor: Interceptor = (next) => async (request) => {
  const slug = accountSlugFromPathname(window.location.pathname)
  if (slug) request.header.set("X-OpenSight-Account-Slug", slug)
  return next(request)
}

export const transport = createConnectTransport({
  baseUrl: "/rpc",
  interceptors: [accountInterceptor],
})
