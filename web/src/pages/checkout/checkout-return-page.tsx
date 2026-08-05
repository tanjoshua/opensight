// /checkout/return is Checkout's success_url target (design 08 "Checkout
// return"): it confirms the session server-side (which reconciles, the same
// path the webhook takes) so the customer sees a paid account without
// waiting on webhook delivery, then continues into onboarding or the app.
// Missing session_id is the abandoned-checkout path — cancel_url already
// points straight at /billing, so this route never even loads for that case,
// but a defensive check keeps a stray direct hit from erroring.
import { Code, ConnectError, createClient } from "@connectrpc/connect"
import { useQueryClient } from "@tanstack/react-query"
import { LoaderCircle } from "lucide-react"
import { useEffect, useState } from "react"
import { Navigate, useSearchParams } from "react-router"

import { transport } from "@/api/transport"
import { BillingService } from "@/gen/opensight/v1/billing_pb"
import { Access } from "@/gen/opensight/v1/common_pb"
import { accountPath } from "@/lib/account-path"

const billingClient = createClient(BillingService, transport)
const CONFIRM_ATTEMPTS = 5

// Stripe and the webhook are normally settled before Checkout redirects, but
// a short propagation delay or transient RPC failure should not send a
// customer who just paid back to a page that still offers Subscribe. Reconcile
// is idempotent, so retrying the same session is safe.
async function confirmCheckoutWithRetry(
  sessionId: string,
  signal: AbortSignal
): Promise<{ access: Access; accountSlug: string }> {
  let access: Access = Access.UNSPECIFIED
  let accountSlug = ""

  for (let attempt = 0; attempt < CONFIRM_ATTEMPTS; attempt++) {
    signal.throwIfAborted()
    try {
      const response = await billingClient.confirmCheckout({ sessionId }, { signal })
      access = response.access
      accountSlug = response.accountSlug
      if (access === Access.FULL) return { access, accountSlug }
    } catch (error) {
      if (!isRetryableConfirmationError(error)) throw error
    }

    if (attempt < CONFIRM_ATTEMPTS - 1) {
      await new Promise((resolve) =>
        window.setTimeout(resolve, 500 * 2 ** attempt)
      )
    }
  }

  return { access, accountSlug }
}

function isRetryableConfirmationError(error: unknown): boolean {
  switch (ConnectError.from(error).code) {
    case Code.InvalidArgument:
    case Code.Unauthenticated:
    case Code.PermissionDenied:
    case Code.NotFound:
    case Code.FailedPrecondition:
      return false
    default:
      return true
  }
}

export function CheckoutReturnPage() {
  const [searchParams] = useSearchParams()
  const sessionId = searchParams.get("session_id")
  const queryClient = useQueryClient()
  const [target, setTarget] = useState<string | undefined>(
    sessionId ? undefined : "/accounts"
  )

  useEffect(() => {
    if (!sessionId) return
    const controller = new AbortController()

    void (async () => {
      let result: { access: Access; accountSlug: string }
      try {
        result = await confirmCheckoutWithRetry(sessionId, controller.signal)
      } catch {
        if (controller.signal.aborted) return
        // Confirmation failed — wrong account, unknown session, or a network
        // failure that outlasted the bounded settling window. The billing
        // page is the safe fallback: it re-derives access itself rather than
        // trusting anything this route decided.
        setTarget("/accounts")
        return
      }

      // The shell (AppLayout, OnboardingPage) reads access off /me, so it
      // must see the reconciled state before we navigate into it.
      await queryClient.invalidateQueries()

      if (!result.accountSlug) {
        setTarget("/accounts")
        return
      }
      setTarget(result.access === Access.FULL ? accountPath(result.accountSlug) : accountPath(result.accountSlug, "/billing"))
    })()

    return () => controller.abort()
  }, [sessionId, queryClient])

  if (target) {
    return <Navigate to={target} replace />
  }

  return (
    <main className="flex min-h-svh flex-col items-center justify-center gap-3 bg-background p-6 text-center">
      <LoaderCircle className="size-6 animate-spin text-muted-foreground" />
      <p className="text-sm text-muted-foreground">Confirming your payment…</p>
    </main>
  )
}
