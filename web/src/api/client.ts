// Typed fetch wrapper for the OpenSight API (design 06): REST/JSON under
// /api/v1, RFC 7807 problem+json errors, cookie sessions (same-origin — the
// Vite dev server proxies /api to the Go server, prod serves both from one
// binary). State-changing requests include X-Requested-With for the CSRF guard.

const BASE_URL = "/api/v1"

export interface Problem {
  type: string
  title: string
  status: number
  detail?: string
}

export class ApiError extends Error {
  readonly problem: Problem

  constructor(problem: Problem) {
    super(problem.detail ?? problem.title)
    this.name = "ApiError"
    this.problem = problem
  }

  get status(): number {
    return this.problem.status
  }
}

export type QueryParams = Record<string, string | number | undefined>

export async function apiGet<T>(
  path: string,
  params?: QueryParams
): Promise<T> {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(params ?? {})) {
    if (value !== undefined) query.set(key, String(value))
  }
  const qs = query.size > 0 ? `?${query.toString()}` : ""

  const res = await fetch(`${BASE_URL}${path}${qs}`, {
    headers: { Accept: "application/json" },
  })
  if (!res.ok) {
    throw new ApiError(await problemFromResponse(res))
  }
  return (await res.json()) as T
}

export async function apiPost<T>(path: string, body?: unknown): Promise<T> {
  return apiWrite<T>("POST", path, body)
}

export async function apiPatch<T>(path: string, body?: unknown): Promise<T> {
  return apiWrite<T>("PATCH", path, body)
}

async function apiWrite<T>(
  method: "POST" | "PATCH",
  path: string,
  body?: unknown
): Promise<T> {
  const headers = new Headers({
    Accept: "application/json",
    "X-Requested-With": "XMLHttpRequest",
  })
  let payload: BodyInit | undefined
  if (body !== undefined) {
    headers.set("Content-Type", "application/json")
    payload = JSON.stringify(body)
  }

  const res = await fetch(`${BASE_URL}${path}`, {
    method,
    headers,
    body: payload,
  })
  if (!res.ok) {
    throw new ApiError(await problemFromResponse(res))
  }
  if (res.status === 204) {
    return undefined as T
  }
  return (await res.json()) as T
}

async function problemFromResponse(res: Response): Promise<Problem> {
  try {
    return (await res.json()) as Problem
  } catch {
    return {
      type: "about:blank",
      title: res.statusText || "request failed",
      status: res.status,
    }
  }
}
