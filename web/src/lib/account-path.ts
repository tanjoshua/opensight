const ACCOUNT_PATH = /^\/a\/([^/]+)/

export function accountSlugFromPathname(pathname: string): string | undefined {
  return pathname.match(ACCOUNT_PATH)?.[1]
}

export function accountPath(slug: string, path = "/overview"): string {
  const suffix = path.startsWith("/") ? path : `/${path}`
  return `/a/${slug}${suffix}`
}

export function useAccountPath() {
  const { accountSlug = "" } = useParams<{ accountSlug: string }>()
  return (path = "/overview") => accountPath(accountSlug, path)
}
import { useParams } from "react-router"
