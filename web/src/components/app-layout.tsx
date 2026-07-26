import { Link, Navigate, Outlet, useLocation } from "react-router"

import { isUnauthenticated } from "@/api/errors"
import { useCurrentBusiness, useMe, useRuns } from "@/api/hooks"
import { AppSidebar } from "@/components/app-sidebar"
import { Badge } from "@/components/ui/badge"
import { Separator } from "@/components/ui/separator"
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { Skeleton } from "@/components/ui/skeleton"

// Shell for the five product sections. /login and /onboarding render outside
// of it.
export function AppLayout() {
  const location = useLocation()
  const me = useMe()
  const { business } = useCurrentBusiness()

  if (me.isLoading) {
    return <AppSkeleton />
  }
  if (isUnauthenticated(me.error)) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }
  if (me.isError || !me.data) {
    return (
      <div className="flex min-h-svh items-center justify-center p-6 text-sm text-muted-foreground">
        The app could not be loaded. Try reloading the page.
      </div>
    )
  }

  return (
    <SidebarProvider>
      <AppSidebar />
      <SidebarInset>
        <header className="flex h-14 shrink-0 items-center gap-2 border-b px-4">
          <SidebarTrigger />
          <Separator orientation="vertical" className="h-4" />
          <span className="font-heading text-sm font-medium">OpenSight</span>
          {business && <RunProgressBadge businessId={business.id} />}
        </header>
        <main className="flex flex-1 flex-col p-6">
          <Outlet />
        </main>
        <footer className="flex flex-wrap gap-x-4 gap-y-2 border-t px-6 py-4 text-xs text-muted-foreground">
          <Link className="hover:text-foreground hover:underline" to="/methodology">
            How we measure
          </Link>
          <Link className="hover:text-foreground hover:underline" to="/privacy">
            Privacy
          </Link>
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
