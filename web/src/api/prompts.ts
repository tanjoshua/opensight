// Prompts-section API module (INS-2, design 06): the active-prompt table with
// each prompt's latest-result summary and spark-trend, and the per-prompt detail
// (full result history + replacement lineage). Every summary carries the
// result_ids behind it so every number is a door (design 06).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiGet, apiPost } from "./client"
import type { PromptResult } from "./responses"

export type PromptStatus = "active" | "retired"

// One point of a prompt's spark-trend: whether the business was mentioned in
// that run, and the result_id door behind it.
export interface PromptTrendPoint {
  run_id: string
  scheduled_for: string // YYYY-MM-DD
  mentioned: boolean
  result_id: string
}

// One row of the Prompts table. order/sentiment/latest_result_id come from the
// latest analyzed result; latest_result_id is null for a prompt with no analyzed
// result yet — the "not yet measured" state, distinct from "measured, not
// mentioned" (mentioned: false with a non-null latest_result_id).
export interface PromptSummary {
  id: string
  text: string
  status: PromptStatus
  mentioned: boolean
  order: number | null
  sentiment: string | null
  latest_result_id: string | null
  trend: PromptTrendPoint[]
}

export interface PromptsResponse {
  prompts: PromptSummary[]
}

// A prompt in a replacement chain. replaces_prompt_id links to the prompt this
// one replaced ("replaced X on date"); created_at is when it started its trend.
export interface PromptLineageNode {
  id: string
  text: string
  status: PromptStatus
  replaces_prompt_id: string | null
  created_at: string
}

// A prompt-detail result: a full result record plus the "not yet analyzed" flag.
export interface PromptDetailResult extends PromptResult {
  unanalyzed: boolean
}

export interface PromptDetail {
  prompt: PromptLineageNode
  lineage: PromptLineageNode[] // the chain this prompt replaced, oldest-last
  results: PromptDetailResult[]
}

export function listPrompts(businessId: string): Promise<PromptsResponse> {
  return apiGet<PromptsResponse>(`/businesses/${businessId}/prompts`)
}

export function getPrompt(promptId: string): Promise<PromptDetail> {
  return apiGet<PromptDetail>(`/prompts/${promptId}`)
}

export function usePrompts(businessId: string | undefined) {
  return useQuery({
    queryKey: ["prompts", businessId],
    queryFn: () => listPrompts(businessId!),
    enabled: businessId !== undefined,
  })
}

export function usePrompt(promptId: string | undefined) {
  return useQuery({
    queryKey: ["prompt", promptId],
    queryFn: () => getPrompt(promptId!),
    enabled: promptId !== undefined,
  })
}

// The add/replace response: the new prompt as a lineage node (a replacement
// carries replaces_prompt_id), so the caller can route to its detail page.
export interface PromptWriteResponse {
  prompt: PromptLineageNode
}

export function addPrompt(
  businessId: string,
  text: string
): Promise<PromptWriteResponse> {
  return apiPost<PromptWriteResponse>(`/businesses/${businessId}/prompts`, {
    text,
  })
}

// replacePrompt always sends confirmed: true — the unskippable warning lives in
// the modal, and the server independently rejects a missing confirmation, so this
// call is only ever reached once the user has confirmed.
export function replacePrompt(
  promptId: string,
  text: string
): Promise<PromptWriteResponse> {
  return apiPost<PromptWriteResponse>(`/prompts/${promptId}/replace`, {
    text,
    confirmed: true,
  })
}

export function useAddPrompt(businessId: string | undefined) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (text: string) => addPrompt(businessId!, text),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["prompts", businessId] })
    },
  })
}

export function useReplacePrompt(businessId: string | undefined) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ promptId, text }: { promptId: string; text: string }) =>
      replacePrompt(promptId, text),
    onSuccess: (_data, { promptId }) => {
      queryClient.invalidateQueries({ queryKey: ["prompts", businessId] })
      queryClient.invalidateQueries({ queryKey: ["prompt", promptId] })
    },
  })
}
