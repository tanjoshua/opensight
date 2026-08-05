// /billing renders standalone (outside AppLayout), alongside /login and
// /onboarding: a never-paid account is denied every classSubscriber/classActive RPC,
// so the product shell has nothing to show it (design 08 "The funnel", BILL-9
// AC "an account that has never paid cannot wander into the app").
import { timestampDate } from "@bufbuild/protobuf/wkt"
import { skipToken, useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { CreditCard, LogOut, RefreshCw } from "lucide-react"
import { type ReactNode } from "react"
import { Link, Navigate, useNavigate } from "react-router"

import { errorMessage, isUnauthenticated } from "@/api/errors"
import { useAccountContext, useMe } from "@/api/hooks"
import { stripeStatusLabel } from "@/api/labels"
import { Access, BusinessStatus } from "@/gen/opensight/v1/common_pb"
import { AccountRole } from "@/gen/opensight/v1/account_pb"
import { accountPath } from "@/lib/account-path"
import {
  BillingAction,
  type GetBillingResponse,
} from "@/gen/opensight/v1/billing_pb"
import {
  createPortalSession,
  getBilling,
  startCheckout,
} from "@/gen/opensight/v1/billing-BillingService_connectquery"
import { logout } from "@/gen/opensight/v1/auth-AuthService_connectquery"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Logo } from "@/components/logo"
import { Skeleton } from "@/components/ui/skeleton"

export function BillingPage() {
  const me = useMe()
  const account = useAccountContext()
  const billing = useQuery(
    getBilling,
    account.data?.role === AccountRole.OWNER ? {} : skipToken
  )

  if (me.isLoading || account.isLoading) {
    return (
      <BillingShell>
        <Skeleton className="h-72 w-full" />
      </BillingShell>
    )
  }
  if (isUnauthenticated(me.error) || isUnauthenticated(account.error) || isUnauthenticated(billing.error)) {
    return <Navigate to="/login" replace />
  }
  if (me.isError || !me.data || account.isError || !account.data?.account) {
    return (
      <BillingShell>
        <Card>
          <CardHeader>
            <CardTitle>Couldn't load billing</CardTitle>
            <CardDescription>
              Something went wrong fetching your account.
            </CardDescription>
          </CardHeader>
          <CardFooter>
            <Button
              type="button"
              onClick={() => {
                void me.refetch()
                void billing.refetch()
              }}
            >
              Try again
            </Button>
          </CardFooter>
        </Card>
      </BillingShell>
    )
  }
  if (account.data.role !== AccountRole.OWNER) {
    return <Navigate to={accountPath(account.data.account.slug)} replace />
  }
  if (billing.isLoading) {
    return (
      <BillingShell>
        <Skeleton className="h-72 w-full" />
      </BillingShell>
    )
  }
  if (billing.isError || !billing.data) {
    return (
      <BillingShell>
        <Card>
          <CardHeader>
            <CardTitle>Couldn't load billing</CardTitle>
            <CardDescription>
              Something went wrong fetching your account.
            </CardDescription>
          </CardHeader>
          <CardFooter>
            <Button type="button" onClick={() => void billing.refetch()}>
              Try again
            </Button>
          </CardFooter>
        </Card>
      </BillingShell>
    )
  }

  const hasActiveBusiness = account.data.businesses.some(
    (b) => b.status !== BusinessStatus.DRAFT
  )

  return (
    <BillingShell>
      <BillingCard data={billing.data} />
      {hasActiveBusiness && (
        <Link
          className="mt-4 self-center text-sm text-muted-foreground hover:text-foreground hover:underline"
          to={accountPath(account.data.account.slug)}
        >
          Back to the app
        </Link>
      )}
    </BillingShell>
  )
}

function BillingCard({ data }: { data: GetBillingResponse }) {
  const checkout = useMutation(startCheckout, {
    onSuccess: (resp) => {
      window.location.href = resp.checkoutUrl
    },
  })
  const portal = useMutation(createPortalSession, {
    onSuccess: (resp) => {
      window.location.href = resp.portalUrl
    },
  })

  const price =
    data.priceUnitAmount > 0n
      ? formatPrice(data.priceUnitAmount, data.priceCurrency)
      : undefined
  const periodEnd = data.currentPeriodEnd
    ? timestampDate(data.currentPeriodEnd)
    : undefined

  const error = checkout.isError
    ? errorMessage(checkout.error, "Couldn't start checkout. Try again.")
    : portal.isError
      ? errorMessage(
          portal.error,
          "Couldn't open billing management. Try again."
        )
      : undefined

  return (
    <Card>
      <CardHeader>
        <CardTitle>{data.plan ? planTitle(data.plan.code) : "Plan"}</CardTitle>
        <CardDescription>
          {price
            ? `${price} / ${data.priceInterval || "month"}`
            : "Pricing unavailable"}
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3 sm:grid-cols-2">
        <BillingValue
          label="Status"
          value={
            data.comped
              ? "Operator-managed"
              : stripeStatusLabel(data.stripeStatus)
          }
        />
        {periodEnd && (
          <BillingValue
            label={periodEndLabel(data.access, data.cancelAtPeriodEnd)}
            value={periodEnd.toLocaleDateString(undefined, {
              year: "numeric",
              month: "long",
              day: "numeric",
            })}
          />
        )}
      </CardContent>
      <CardFooter className="flex flex-col items-stretch gap-3">
        {error && (
          <p className="text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
        {data.comped ? (
          <p className="text-sm text-muted-foreground">
            Your billing is managed by OpenSight. Contact us with any questions.
          </p>
        ) : data.action === BillingAction.CHECKOUT ? (
          <Button
            type="button"
            disabled={checkout.isPending}
            onClick={() => checkout.mutate({})}
          >
            {data.access === Access.NEVER ? (
              <CreditCard data-icon="inline-start" />
            ) : (
              <RefreshCw data-icon="inline-start" />
            )}
            {checkout.isPending
              ? "Starting checkout"
              : data.access === Access.NEVER
                ? "Subscribe"
                : "Reactivate"}
          </Button>
        ) : data.action === BillingAction.PORTAL ? (
          <Button
            type="button"
            variant="outline"
            disabled={portal.isPending}
            onClick={() => portal.mutate({})}
          >
            <CreditCard data-icon="inline-start" />
            {portal.isPending ? "Opening billing" : "Manage billing"}
          </Button>
        ) : (
          <p className="text-sm text-muted-foreground">
            Contact OpenSight for help with billing.
          </p>
        )}
      </CardFooter>
    </Card>
  )
}

// periodEndLabel picks AC3's "renewal or end date" wording off access first,
// not cancel_at_period_end alone: once a subscription actually finishes
// canceling, Stripe's cancel_at_period_end reverts to false and
// current_period_end is left holding the last (now past) period — labeling
// that "Renews" would show a stale date as if it were still counting down.
// Access is the authoritative word on whether the subscription is still
// live; cancel_at_period_end only distinguishes the two live sub-cases.
function periodEndLabel(access: Access, cancelAtPeriodEnd: boolean): string {
  if (access === Access.LAPSED) return "Access ended"
  return cancelAtPeriodEnd ? "Access ends" : "Renews"
}

function BillingValue({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="font-medium">{value}</p>
    </div>
  )
}

// planTitle renders a catalog plan code (e.g. "starter") as a display title
// ("Starter"). The Plan message carries no separate display name — the
// catalog's Name field (internal/billing) isn't on the wire — so this is the
// SPA's one translation, not a second source of truth for it.
function planTitle(code: string): string {
  return code.length > 0 ? code[0].toUpperCase() + code.slice(1) : "Plan"
}

// formatPrice never hardcodes a currency symbol: Intl.NumberFormat renders
// whatever currency Stripe actually returns (design 08 — the displayed price
// can never disagree with the charge).
function formatPrice(unitAmount: bigint, currency: string): string {
  const amount = Number(unitAmount) / 100
  try {
    return new Intl.NumberFormat(undefined, {
      style: "currency",
      currency: currency.toUpperCase(),
    }).format(amount)
  } catch {
    return `${amount.toFixed(2)} ${currency.toUpperCase()}`
  }
}

function BillingShell({ children }: { children: ReactNode }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const logoutMutation = useMutation(logout, {
    onSettled: () => {
      queryClient.clear()
      navigate("/login", { replace: true })
    },
  })

  return (
    <main className="flex min-h-svh flex-col items-center bg-background p-6">
      <div className="flex w-full max-w-md items-center gap-2 py-2">
        <Logo className="size-5" />
        <span className="font-heading text-sm font-medium">OpenSight</span>
        <Button
          className="ms-auto"
          variant="ghost"
          size="sm"
          disabled={logoutMutation.isPending}
          onClick={() => logoutMutation.mutate({})}
        >
          <LogOut data-icon="inline-start" />
          Log out
        </Button>
      </div>
      <div className="mt-6 flex w-full max-w-md flex-col">{children}</div>
    </main>
  )
}
