// Competitors-section API module (INS-3, design 06): the read + compare payload —
// the business's own coverage (the baseline) plus every competitor's comparison
// stats (mention %, totals, avg order, per-prompt appearances, weekly trend,
// vs-self), coverage-desc ranked. Every aggregate carries the result_ids behind
// it so every number is a door (design 06).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiGet, apiPatch, apiPost, type QueryParams } from "./client"

const PAGE_SIZE = 100

export type CompetitorStatus = "discovered" | "tracked" | "dismissed"

// The business's own coverage over the shared analyzed base — the baseline each
// competitor's vs_self is measured against.
export interface CompetitorSelf {
  total_analyzed: number
  mentioned: number
  percent: number
  result_ids: string[]
}

// "In N of the responses to this prompt": the analyzed results of one prompt in
// which the competitor appeared. Coverage is result_ids.length.
export interface CompetitorPromptAppearance {
  prompt_id: string
  prompt_text: string
  result_ids: string[]
}

// A competitor's mention % for one analyzed run, over the same per-run
// denominator as the business's own visibility that week. Oldest-first.
export interface CompetitorTrendPoint {
  run_id: string
  scheduled_for: string // YYYY-MM-DD
  analyzed: number
  mentioned: number
  percent: number
  result_ids: string[]
}

export interface Competitor {
  id: string
  name: string
  status: CompetitorStatus
  aliases: string[]
  suggested_aliases: string[]
  mentioned: number // distinct analyzed results mentioning this competitor
  total_mentions: number
  mention_percent: number
  avg_order: number
  vs_self: number // mention_percent - self.percent
  result_ids: string[]
  per_prompt: CompetitorPromptAppearance[]
  trend: CompetitorTrendPoint[]
}

export interface CompetitorsResponse {
  self: CompetitorSelf
  competitors: Competitor[] // coverage-desc ranked
  paging: { limit: number; offset: number; page_count: number }
}

export interface CompetitorsFilter {
  status?: CompetitorStatus
  limit?: number
  offset?: number
}

export interface CompetitorWriteResponse {
  id: string
  name: string
  website: string | null
  aliases: string[]
  suggested_aliases: string[]
  source: "discovered" | "manual"
  status: CompetitorStatus
}

export interface AddCompetitorInput {
  name: string
  aliases?: string[]
  website?: string
}

export function listCompetitors(
  businessId: string,
  filter: CompetitorsFilter = {}
): Promise<CompetitorsResponse> {
  const params: QueryParams = {
    status: filter.status,
    limit: filter.limit,
    offset: filter.offset,
  }
  return apiGet<CompetitorsResponse>(
    `/businesses/${businessId}/competitors`,
    params
  )
}

export async function listAllCompetitors(
  businessId: string,
  filter: Pick<CompetitorsFilter, "status"> = {}
): Promise<CompetitorsResponse> {
  let page = await listCompetitors(businessId, {
    ...filter,
    limit: PAGE_SIZE,
    offset: 0,
  })
  const self = page.self
  const competitors = [...page.competitors]
  let offset = page.paging.page_count

  while (page.paging.page_count === PAGE_SIZE) {
    page = await listCompetitors(businessId, {
      ...filter,
      limit: PAGE_SIZE,
      offset,
    })
    if (page.paging.page_count === 0) break
    competitors.push(...page.competitors)
    offset += page.paging.page_count
    if (page.paging.page_count < PAGE_SIZE) break
  }

  return {
    self,
    competitors,
    paging: { limit: PAGE_SIZE, offset: 0, page_count: competitors.length },
  }
}

export function useCompetitors(
  businessId: string | undefined,
  filter: CompetitorsFilter = {}
) {
  return useQuery({
    queryKey: ["competitors", businessId, filter],
    queryFn: () => listCompetitors(businessId!, filter),
    enabled: businessId !== undefined,
  })
}

export function useAllCompetitors(
  businessId: string | undefined,
  filter: Pick<CompetitorsFilter, "status"> = {}
) {
  return useQuery({
    queryKey: ["competitors", "all", businessId, filter],
    queryFn: () => listAllCompetitors(businessId!, filter),
    enabled: businessId !== undefined,
  })
}

export function addCompetitor(
  businessId: string,
  input: AddCompetitorInput
): Promise<CompetitorWriteResponse> {
  return apiPost(`/businesses/${businessId}/competitors`, input)
}

export function setCompetitorStatus(
  competitorId: string,
  status: "tracked" | "dismissed"
): Promise<CompetitorWriteResponse> {
  const action = status === "tracked" ? "track" : "dismiss"
  return apiPost(`/competitors/${competitorId}/${action}`)
}

export function reviewSuggestedAlias(
  competitorId: string,
  alias: string,
  action: "approve" | "reject"
): Promise<CompetitorWriteResponse> {
  return apiPost(`/competitors/${competitorId}/suggested-aliases/${action}`, {
    alias,
  })
}

export function updateCompetitorAliases(
  competitorId: string,
  aliases: string[]
): Promise<CompetitorWriteResponse> {
  return apiPatch(`/competitors/${competitorId}`, { aliases })
}

function useInvalidateCompetitorViews(businessId: string | undefined) {
  const queryClient = useQueryClient()
  return () => {
    queryClient.invalidateQueries({ queryKey: ["competitors"] })
    queryClient.invalidateQueries({ queryKey: ["overview", businessId] })
  }
}

export function useAddCompetitor(businessId: string | undefined) {
  const invalidate = useInvalidateCompetitorViews(businessId)
  return useMutation({
    mutationFn: (input: AddCompetitorInput) =>
      addCompetitor(businessId!, input),
    onSuccess: invalidate,
  })
}

export function useSetCompetitorStatus(businessId: string | undefined) {
  const invalidate = useInvalidateCompetitorViews(businessId)
  return useMutation({
    mutationFn: ({
      competitorId,
      status,
    }: {
      competitorId: string
      status: "tracked" | "dismissed"
    }) => setCompetitorStatus(competitorId, status),
    onSuccess: invalidate,
  })
}

export function useReviewSuggestedAlias(businessId: string | undefined) {
  const invalidate = useInvalidateCompetitorViews(businessId)
  return useMutation({
    mutationFn: ({
      competitorId,
      alias,
      action,
    }: {
      competitorId: string
      alias: string
      action: "approve" | "reject"
    }) => reviewSuggestedAlias(competitorId, alias, action),
    onSettled: invalidate,
  })
}

export function useUpdateCompetitorAliases(businessId: string | undefined) {
  const invalidate = useInvalidateCompetitorViews(businessId)
  return useMutation({
    mutationFn: ({
      competitorId,
      aliases,
    }: {
      competitorId: string
      aliases: string[]
    }) => updateCompetitorAliases(competitorId, aliases),
    onSuccess: invalidate,
  })
}
