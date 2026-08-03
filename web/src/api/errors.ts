// Connect RPC error helpers (RPC-7): ConnectError.message is code-prefixed
// (e.g. "[unauthenticated] authentication required") and must never reach the
// UI — rawMessage is the server's plain detail string.
import { Code, ConnectError } from "@connectrpc/connect"

import { AccessDeniedSchema, type Access } from "@/gen/opensight/v1/common_pb"

export function errorMessage(err: unknown, fallback: string): string {
  return ConnectError.from(err).rawMessage || fallback
}

export function isUnauthenticated(err: unknown): boolean {
  return ConnectError.from(err).code === Code.Unauthenticated
}

// accessDeniedFrom extracts the RPC access gate's AccessDenied detail (BILL-6):
// the billing state that caused the rejection, so the SPA renders the right
// state rather than parsing the error message. Returns undefined for any
// error that isn't a gate rejection (wrong code, or no detail attached).
export function accessDeniedFrom(err: unknown): Access | undefined {
  const details = ConnectError.from(err).findDetails(AccessDeniedSchema)
  return details[0]?.access
}
