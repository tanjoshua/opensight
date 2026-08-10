import { useNavigate, useParams } from "react-router"

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

// Every in-app destination lives under /a/:accountSlug, so navigating with a
// bare "/runs/x" escapes the workspace and falls through to /accounts. Pages
// inside the shell should always navigate through this hook.
export function useAccountNavigate() {
  const navigate = useNavigate()
  const path = useAccountPath()
  return (to: string, options?: { replace?: boolean }) =>
    navigate(path(to), options)
}
