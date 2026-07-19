// Responses-section API module: runs and prompt results (design 06,
// Phase 1 shapes from internal/api/responses.go). Query hooks for the
// Responses pages arrive with WEB-3/4/5.
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
  prompt?: PromptSummary
  run?: Run
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
  return apiGet<ResultsResponse>(`/businesses/${businessId}/results`, {
    ...filter,
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
