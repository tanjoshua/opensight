// Auth/session API module. Read-only for now: the login/logout mutations and
// their X-Requested-With wiring land with AUTH-4.
import { useQuery } from "@tanstack/react-query"

import { ApiError, apiGet } from "./client"

export interface User {
  id: string
  email: string
}

export interface Tenant {
  id: string
  name: string
}

export interface UserTenant {
  user: User
  tenant: Tenant
}

export function getMe(): Promise<UserTenant> {
  return apiGet<UserTenant>("/me")
}

// useMe doubles as the smoke test that TanStack Query + the /api proxy work.
// A 401 just means "not logged in" — never retry it.
export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: getMe,
    retry: (failureCount, error) =>
      !(error instanceof ApiError && error.status === 401) && failureCount < 2,
  })
}
