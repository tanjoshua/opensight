// Hand-written hooks connect-query cannot generate (RPC-7):
//   - useMe: the one call site for AuthService.GetMe, so every consumer shares
//     one cache key (an explicit `{}` input vs. an omitted one produces
//     different keys — see the module doc below).
//   - useAllCompetitors: the client-side pagination loop that walks every page
//     of ListCompetitors; connect-query has no built-in equivalent.
//   - useInvalidateCompetitorViews: the shared invalidation used by every
//     competitor-mutating page — service-level keys so it also catches
//     useAllCompetitors's hand-written query key below.
//   - useRuns / pollWhileRunning: the "poll while a run is in progress"
//     behavior the shell badge, Runs and Overview all need.
//   - useAccountContext: the URL-selected account, role, businesses, billing
//     access, and plan used throughout the product shell.
//   - usePlan: the useMe projection for plan entitlements (BILL-6 — GetMe
//     carries Plan, BusinessProfile no longer does), narrowing the
//     possibly-undefined field once so callers never fall back to a bogus
//     default limit.
//   - useBillingAccess: the useAccountContext projection for the account's derived
//     billing access (BILL-10) — the one primitive every spend-side
//     safeguard in the SPA should ask, rather than comparing Access inline.
import { Code, createClient } from "@connectrpc/connect"
import {
  createConnectQueryKey,
  skipToken,
  useQuery,
} from "@connectrpc/connect-query"
import {
  useQuery as useTanstackQuery,
  useQueryClient,
} from "@tanstack/react-query"

import { getMe } from "@/gen/opensight/v1/auth-AuthService_connectquery"
import { getAccountContext } from "@/gen/opensight/v1/account-AccountService_connectquery"
import type { BusinessSummary, Plan } from "@/gen/opensight/v1/common_pb"
import { Access, RunStatus } from "@/gen/opensight/v1/common_pb"
import {
  CompetitorService,
  type Competitor,
  type CompetitorSelf,
} from "@/gen/opensight/v1/competitor_pb"
import { OverviewService } from "@/gen/opensight/v1/overview_pb"
import { listRuns } from "@/gen/opensight/v1/result-ResultService_connectquery"
import type { ListRunsResponse, Run } from "@/gen/opensight/v1/result_pb"
import { transport } from "./transport"
import { useParams } from "react-router"

// getMe's request is an empty message: useQuery(getMe) (no input) and
// useQuery(getMe, {}) (explicit empty input) produce different cache keys,
// since createConnectQueryKey only includes `input` in the key when the
// caller passed one. Every consumer must go through this one wrapper so the
// key is consistent app-wide.
export function useMe() {
  return useQuery(getMe, {}, {
    retry: (failureCount, error) =>
      error.code !== Code.Unauthenticated && failureCount < 2,
  })
}

export function useAccountContext() {
  const { accountSlug } = useParams<{ accountSlug: string }>()
  return useQuery(
    getAccountContext,
    accountSlug ? { accountSlug } : skipToken,
    {
      retry: (failureCount, error) =>
        error.code !== Code.Unauthenticated &&
        error.code !== Code.PermissionDenied &&
        error.code !== Code.NotFound &&
        failureCount < 2,
    }
  )
}

export interface CurrentBusiness {
  business: BusinessSummary | undefined
  businessId: string | undefined
  isLoading: boolean
  isError: boolean
  // Distinguishes "still loading" from "loaded, but there's no business yet".
  isReady: boolean
}

export function useCurrentBusiness(): CurrentBusiness {
  const account = useAccountContext()
  const business = account.data?.businesses[0]
  return {
    business,
    businessId: business?.id,
    isLoading: account.isLoading,
    isError: account.isError,
    isReady: account.data !== undefined,
  }
}

export interface CurrentPlan {
  plan: Plan | undefined
  isLoading: boolean
  isError: boolean
  // Distinguishes "still loading" from "loaded, with a plan" — narrows
  // GetMeResponse.plan's possible-undefined shape once, so call sites (e.g.
  // onboarding's prompt-count validation) never fall back to a bogus default
  // limit like 0 (BILL-6).
  isReady: boolean
}

export function usePlan(): CurrentPlan {
  const account = useAccountContext()
  return {
    plan: account.data?.plan,
    isLoading: account.isLoading,
    isError: account.isError,
    isReady: account.data?.plan !== undefined,
  }
}

export interface CurrentBillingAccess {
  access: Access
  isActive: boolean // Access.FULL
  isLapsed: boolean
  isReady: boolean
}

// useBillingAccess is the one billing primitive the SPA asks (BILL-10).
// Named after the subscription, not after any single feature, so a future
// safeguard reuses this rather than growing its own Access comparison.
export function useBillingAccess(): CurrentBillingAccess {
  const account = useAccountContext()
  return {
    access: account.data?.access ?? Access.UNSPECIFIED,
    isActive: account.data?.access === Access.FULL,
    isLapsed: account.data?.access === Access.LAPSED,
    isReady: account.data !== undefined,
  }
}

const competitorClient = createClient(CompetitorService, transport)
const PAGE_SIZE = 100

export interface AllCompetitors {
  self: CompetitorSelf | undefined
  competitors: Competitor[]
}

async function listAll(
  businessId: string,
  signal: AbortSignal | undefined
): Promise<AllCompetitors> {
  let page = await competitorClient.listCompetitors(
    { businessId, limit: PAGE_SIZE, offset: 0 },
    { signal }
  )
  const self = page.self
  const competitors = [...page.competitors]
  let offset = page.paging?.pageCount ?? 0

  while ((page.paging?.pageCount ?? 0) === PAGE_SIZE) {
    page = await competitorClient.listCompetitors(
      { businessId, limit: PAGE_SIZE, offset },
      { signal }
    )
    const count = page.paging?.pageCount ?? 0
    if (count === 0) break
    competitors.push(...page.competitors)
    offset += count
    if (count < PAGE_SIZE) break
  }

  return { self, competitors }
}

export function useAllCompetitors(businessId: string | undefined) {
  return useTanstackQuery({
    queryKey: [
      ...createConnectQueryKey({ schema: CompetitorService, cardinality: undefined }),
      "all",
      businessId,
    ],
    queryFn:
      businessId === undefined
        ? skipToken
        : ({ signal }) => listAll(businessId, signal),
  })
}

// A run in progress is the one thing the UI polls for (design 06): refetch
// every few seconds while one is executing, and stop as soon as none is.
export const RUN_POLL_INTERVAL_MS = 5000

export const isRunning = (run: Run) => run.status === RunStatus.RUNNING

// refetchInterval for any query whose data can say "a run is still executing".
// Used by useRuns below and by Overview, whose own poll keys off GetOverview's
// latest_run rather than the runs list.
export function pollWhileRunning<T>(running: (data: T) => boolean) {
  return (query: { state: { data: T | undefined } }) =>
    query.state.data !== undefined && running(query.state.data)
      ? RUN_POLL_INTERVAL_MS
      : false
}

// Runs for a business, polling while any of them is in progress. hasRunningRun
// is returned alongside so call sites don't re-derive it.
export function useRuns(businessId: string | undefined) {
  const query = useQuery(
    listRuns,
    businessId === undefined ? skipToken : { businessId },
    {
      refetchInterval: pollWhileRunning((data: ListRunsResponse) =>
        data.runs.some(isRunning)
      ),
    }
  )
  return {
    ...query,
    hasRunningRun: query.data?.runs.some(isRunning) ?? false,
  }
}

export function useInvalidateCompetitorViews() {
  const qc = useQueryClient()
  return () => {
    void qc.invalidateQueries({
      queryKey: createConnectQueryKey({ schema: CompetitorService, cardinality: undefined }),
    })
    void qc.invalidateQueries({
      queryKey: createConnectQueryKey({ schema: OverviewService, cardinality: undefined }),
    })
  }
}
