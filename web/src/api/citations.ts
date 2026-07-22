// Citation-sources API module (MET-6): domains, cited pages, prompt associations,
// and subject splits. Every aggregate carries result_ids for the shared drawer.
import { useQuery } from "@tanstack/react-query"

import { apiGet, type QueryParams } from "./client"
import type { Paging } from "./responses"

export interface CitationSubjectStat {
  frequency: number
  result_ids: string[]
}

export interface CitationSubjects {
  business: CitationSubjectStat
  competitor: CitationSubjectStat
  other: CitationSubjectStat
  unknown: CitationSubjectStat
}

export interface CitationPage {
  url: string
  title: string | null
  frequency: number
  subjects: CitationSubjects
  result_ids: string[]
}

export interface CitationPrompt {
  prompt_id: string
  prompt_text: string
  frequency: number
  result_ids: string[]
}

export interface CitationSource {
  domain: string
  frequency: number
  subjects: CitationSubjects
  result_ids: string[]
  pages: CitationPage[]
  prompts: CitationPrompt[]
}

export interface CitationSourcesResponse {
  domains: CitationSource[]
  paging: Paging
}

export interface CitationSourcesFilter {
  domain?: string
  limit?: number
  offset?: number
}

export function listCitationSources(
  businessId: string,
  filter: CitationSourcesFilter = {}
): Promise<CitationSourcesResponse> {
  const params: QueryParams = {
    domain: filter.domain,
    limit: filter.limit,
    offset: filter.offset,
  }
  return apiGet<CitationSourcesResponse>(
    `/businesses/${businessId}/citations`,
    params
  )
}

export function useCitationSources(
  businessId: string | undefined,
  filter: CitationSourcesFilter = {}
) {
  return useQuery({
    queryKey: ["citations", businessId, filter],
    queryFn: () => listCitationSources(businessId!, filter),
    enabled: businessId !== undefined,
  })
}
