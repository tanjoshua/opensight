import { useQuery } from "@tanstack/react-query"

import { ApiError, apiGet, apiPost } from "./client"

export interface User {
  id: string
  email: string
}

export interface Tenant {
  id: string
  name: string
}

export interface BusinessSummary {
  id: string
  name: string
  status: string
}

export interface UserTenant {
  user: User
  tenant: Tenant
}

// /me additionally lists the tenant's businesses so pages can resolve the
// business-scoped API URLs (design 06: MVP is one business per tenant).
export interface Me extends UserTenant {
  businesses: BusinessSummary[]
  prompt_limit: number
}

export interface LoginRequest {
  email: string
  password: string
}

export function getMe(): Promise<Me> {
  return apiGet<Me>("/me")
}

export function login(req: LoginRequest): Promise<UserTenant> {
  return apiPost<UserTenant>("/login", req)
}

export function logout(): Promise<void> {
  return apiPost<void>("/logout")
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
