// Overview-section API module (design 06): the single payload behind the
// Overview page — headline visibility + weekly trend, the three compact panels
// (keywords, cited domains, competitors), prompt-change markers, and latest run
// status. Every aggregate carries its result_ids so every number is a door.
import { useQuery } from "@tanstack/react-query"

import { apiGet } from "./client"
import type { CompetitorTrendPoint } from "./competitors"
import type { Run } from "./responses"

export interface VisibilityPoint {
  run_id: string
  scheduled_for: string // YYYY-MM-DD
  analyzed: number
  mentioned: number
  percent: number
  result_ids: string[]
}

export interface OverviewVisibility {
  current: number | null
  delta: number | null
  trend: VisibilityPoint[] // oldest-first
}

export interface KeywordStat {
  keyword: string
  result_ids: string[]
}

export interface DomainStat {
  domain: string
  result_ids: string[]
}

export type CompetitorStatus = "tracked" | "discovered"

export interface CompetitorSummary {
  id: string
  name: string
  status: CompetitorStatus
  mentioned: number
  total_mentions: number
  mention_percent: number
  avg_order: number
  vs_self: number
  result_ids: string[]
  // Weekly mention %, aligned point-for-point with the business's own visibility
  // trend (same analyzed runs), so the Overview chart can plot this competitor as
  // a line against the "You" line.
  trend: CompetitorTrendPoint[]
}

// PromptChange is one day the prompt set changed and what changed on it, so the
// trend marker at that date can name the change (design 06).
export interface PromptChange {
  date: string // YYYY-MM-DD
  added: number
  retired: number
  replaced: number
}

export interface Overview {
  visibility: OverviewVisibility
  prompt_changes: PromptChange[]
  top_keywords: KeywordStat[]
  top_cited_domains: DomainStat[]
  top_competitors: CompetitorSummary[] // truncated to top-3 discovered
  discovered_total: number // full discovered (untriaged) count, before truncation
  latest_run: Run | null
}

export function getOverview(businessId: string): Promise<Overview> {
  return apiGet<Overview>(`/businesses/${businessId}/overview`)
}

// While the latest run is still executing (first-run-in-progress, design 06),
// poll so the headline and trend fill in without a manual reload.
export function useOverview(businessId: string | undefined) {
  return useQuery({
    queryKey: ["overview", businessId],
    queryFn: () => getOverview(businessId!),
    enabled: businessId !== undefined,
    refetchInterval: (query) =>
      query.state.data?.latest_run?.status === "running" ? 5000 : false,
  })
}
