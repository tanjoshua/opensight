// Responses-section API module: runs and prompt results (design 06,
// Phase 1 shapes from internal/api/responses.go).
import { keepPreviousData, useQuery } from "@tanstack/react-query"

import { apiGet } from "./client"

export interface Run {
  id: string
  business_id: string
  platform: string
  trigger: string
  scheduled_for: string
  status: string
  workflow_id: string
  started_at: string
  completed_at: string | null
  analysis_completed_at: string | null
  visibility?: number | null
}

export interface RunsResponse {
  runs: Run[]
}

export type ResultStatus = "succeeded" | "failed"

export interface PromptSummary {
  id: string
  text: string
}

export interface PromptResult {
  id: string
  run_id: string
  prompt_id: string
  status: string
  model: string | null
  request?: unknown
  raw_response?: unknown
  response_text: string | null
  error: string | null
  requested_at: string
  completed_at: string
  unanalyzed: boolean
  prompt?: PromptSummary
  run?: Run
  analysis?: ResultAnalysis
}

export interface ResultAnalysis {
  sentiment: string | null
  keywords: string[]
  excerpts: string[]
  mentions: ResultMention[]
  citations: ResultCitation[]
}

export interface ResultMention {
  subject: "self" | "competitor" | string
  verbatim_name: string
  order: number
  matched_by: string
  excerpt: string
}

export interface ResultCitation {
  url: string
  domain: string
  title: string | null
  cite_order: number
  subject: "business" | "competitor" | "other" | "unknown" | string
  span: ResultSpan | null
}

export interface ResultSpan {
  start: number
  end: number
}

export interface Paging {
  limit: number
  offset: number
  page_count: number
}

export interface ResultsResponse {
  results: PromptResult[]
  paging: Paging
}

export interface ResultFilter {
  run?: string
  prompt?: string
  status?: ResultStatus
  mentioned?: boolean
  limit?: number
  offset?: number
}

export function listRuns(businessId: string): Promise<RunsResponse> {
  return apiGet<RunsResponse>(`/businesses/${businessId}/runs`)
}

export function listResults(
  businessId: string,
  filter: ResultFilter = {}
): Promise<ResultsResponse> {
  const { mentioned, ...rest } = filter
  return apiGet<ResultsResponse>(`/businesses/${businessId}/results`, {
    ...rest,
    mentioned: mentioned === undefined ? undefined : String(mentioned),
  })
}

export function getResult(
  resultId: string,
  opts: { includeRaw?: boolean } = {}
): Promise<PromptResult> {
  return apiGet<PromptResult>(`/results/${resultId}`, {
    include_raw: opts.includeRaw ? "true" : undefined,
  })
}

export function useRuns(businessId: string | undefined) {
  return useQuery({
    queryKey: ["runs", businessId],
    queryFn: () => listRuns(businessId!),
    enabled: businessId !== undefined,
    refetchInterval: (query) =>
      query.state.data?.runs.some((run) => run.status === "running")
        ? 5000
        : false,
  })
}

// keepPreviousData holds the current page on screen while the next page or
// filter combination loads, so paging never flashes an empty table.
export function useResults(
  businessId: string | undefined,
  filter: ResultFilter,
  opts: { poll?: boolean } = {}
) {
  return useQuery({
    queryKey: ["results", businessId, filter],
    queryFn: () => listResults(businessId!, filter),
    enabled: businessId !== undefined,
    placeholderData: keepPreviousData,
    refetchInterval: opts.poll ? 5000 : false,
  })
}

export function useResult(
  resultId: string | undefined,
  opts: { includeRaw?: boolean } = {}
) {
  return useQuery({
    queryKey: ["result", resultId, opts.includeRaw === true],
    queryFn: () => getResult(resultId!, { includeRaw: opts.includeRaw }),
    enabled: resultId !== undefined,
    placeholderData: keepPreviousData,
  })
}
