import { Link, Navigate, Outlet, useLocation } from "react-router"

import { isMissingAccountAccess, isUnauthenticated } from "@/api/errors"
import {
  useAccountContext,
  useCurrentBusiness,
  useMe,
  useRuns,
} from "@/api/hooks"
import { Access, BusinessStatus } from "@/gen/opensight/v1/common_pb"
import { AccountRole } from "@/gen/opensight/v1/account_pb"
import { accountPath } from "@/lib/account-path"
import { AppSidebar } from "@/components/app-sidebar"
import { BillingBanner } from "@/components/billing-banner"
import { Badge } from "@/components/ui/badge"
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { Skeleton } from "@/components/ui/skeleton"

// Shared shell for account-scoped product and workspace pages. /login and
// /onboarding render outside it.
export function AppLayout() {
  const location = useLocation()
  const me = useMe()
  const account = useAccountContext()
  const { business } = useCurrentBusiness()

  if (me.isLoading || account.isLoading) {
    return <AppSkeleton />
  }
  if (isUnauthenticated(me.error)) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }
  if (isMissingAccountAccess(account.error)) {
    return <Navigate to="/accounts" replace />
  }
  if (me.isError || !me.data || account.isError || !account.data) {
    return (
      <div className="flex min-h-svh items-center justify-center p-6 text-sm text-muted-foreground">
        The app could not be loaded. Try reloading the page.
      </div>
    )
  }
  const slug = account.data.account?.slug
  if (!slug) return <Navigate to="/accounts" replace />
  const accountName = account.data.account?.name ?? "OpenSight"
  const workspaceRoutes = ["/billing", "/team", "/methodology", "/privacy"]
  const isWorkspaceRoute = workspaceRoutes.some(
    (path) => location.pathname === accountPath(slug, path)
  )

  // Workspace pages remain useful before subscription and onboarding. Only
  // business-product routes are gated, so Billing never loses the app shell.
  if (account.data.access === Access.NEVER && !isWorkspaceRoute) {
    return (
      <Navigate
        to={accountPath(
          slug,
          account.data.role === AccountRole.OWNER ? "/billing" : "/team"
        )}
        replace
      />
    )
  }
  // Onboarding is account-scoped (a teammate joining an already-onboarded org
  // has an active business the moment they log in), so this checks every
  // business on the account, not just useCurrentBusiness's first entry.
  const hasActiveBusiness = account.data.businesses.some(
    (b) => b.status !== BusinessStatus.DRAFT
  )
  if (!hasActiveBusiness && !isWorkspaceRoute) {
    return account.data.role === AccountRole.OWNER ||
      account.data.role === AccountRole.ADMIN ? (
      <Navigate to={accountPath(slug, "/onboarding")} replace />
    ) : (
      <Navigate to={accountPath(slug, "/team")} replace />
    )
  }

  return (
    <SidebarProvider>
      <AppSidebar />
      <SidebarInset>
        <header className="flex h-14 shrink-0 items-center gap-2 border-b px-4">
          <SidebarTrigger />
          <span className="min-w-0 truncate text-sm font-medium md:hidden">
            {business?.name ?? accountName}
          </span>
          {business && <RunProgressBadge businessId={business.id} />}
        </header>
        <BillingBanner />
        <main className="flex flex-1 flex-col px-4 py-6 md:px-6">
          <div className="mx-auto flex w-full max-w-[1180px] flex-1 flex-col">
            <Outlet />
          </div>
        </main>
        <footer className="border-t px-4 py-4 md:px-6">
          <div className="mx-auto flex w-full max-w-[1180px] flex-wrap gap-x-4 gap-y-2 text-xs text-muted-foreground">
            <Link
              className="hover:text-foreground hover:underline"
              to={accountPath(slug, "/methodology")}
            >
              How we measure
            </Link>
            <Link
              className="hover:text-foreground hover:underline"
              to={accountPath(slug, "/privacy")}
            >
              Privacy
            </Link>
          </div>
        </footer>
      </SidebarInset>
    </SidebarProvider>
  )
}

function RunProgressBadge({ businessId }: { businessId: string }) {
  const { hasRunningRun } = useRuns(businessId)

  if (!hasRunningRun) return null
  return (
    <Badge variant="outline" className="ms-auto">
      Run in progress
    </Badge>
  )
}

function AppSkeleton() {
  return (
    <div className="flex min-h-svh">
      <div className="hidden w-64 border-r p-3 md:flex md:flex-col md:gap-3">
        <Skeleton className="h-8 w-36" />
        {Array.from({ length: 5 }, (_, i) => (
          <Skeleton key={i} className="h-7 w-full" />
        ))}
      </div>
      <div className="flex flex-1 flex-col">
        <div className="flex h-14 items-center gap-2 border-b px-4">
          <Skeleton className="h-7 w-7" />
          <Skeleton className="h-4 w-24" />
        </div>
        <div className="flex flex-col gap-3 p-6">
          <Skeleton className="h-8 w-40" />
          <Skeleton className="h-52 w-full" />
        </div>
      </div>
    </div>
  )
}
