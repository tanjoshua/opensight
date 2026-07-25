// Hand-written hooks connect-query cannot generate (RPC-7):
//   - useMe: the one call site for AuthService.GetMe, so every consumer shares
//     one cache key (an explicit `{}` input vs. an omitted one produces
//     different keys — see the module doc below).
//   - useAllCompetitors: the client-side pagination loop that walks every page
//     of ListCompetitors; connect-query has no built-in equivalent.
//   - useInvalidateCompetitorViews: the shared invalidation used by every
//     competitor-mutating page — service-level keys so it also catches
//     useAllCompetitors's hand-written query key below.
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
import {
  CompetitorService,
  type Competitor,
  type CompetitorSelf,
} from "@/gen/opensight/v1/competitor_pb"
import { OverviewService } from "@/gen/opensight/v1/overview_pb"
import { transport } from "./transport"

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
