// Connect RPC error helpers (RPC-7): ConnectError.message is code-prefixed
// (e.g. "[unauthenticated] invalid email or password") and must never reach the
// UI — rawMessage is the server's plain detail string.
import { Code, ConnectError } from "@connectrpc/connect"

export function errorMessage(err: unknown, fallback: string): string {
  return ConnectError.from(err).rawMessage || fallback
}

export function isUnauthenticated(err: unknown): boolean {
  return ConnectError.from(err).code === Code.Unauthenticated
}
