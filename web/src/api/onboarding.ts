// Onboarding-section API module (ONB-5, design 03): create a draft business,
// poll its generation proposal, regenerate while draft, and apply the final
// user-edited payload. The proposal shapes mirror internal/llm/propose_profile.go
// (ProposalPayload and friends) verbatim, since apply takes the payload as-is —
// the server does not merge (design 03, "Review and apply").
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiGet, apiPost } from "./client"

// Prompt kinds are a closed set (design 03, prompt generation rules).
export const PROMPT_KINDS = [
  "category",
  "service",
  "condition",
  "location",
] as const
export type PromptKind = (typeof PROMPT_KINDS)[number]

export interface ProposedLocation {
  address: string
  area: string
  city: string
  country: string
}

export interface ProposedProfile {
  name: string
  aliases: string[]
  category: string
  services: string[]
  location: ProposedLocation
}

export interface ProposedPrompt {
  text: string
  kind: string
}

export interface ProposalPayload {
  low_confidence: boolean
  profile: ProposedProfile
  prompts: ProposedPrompt[]
}

// Generation status from GET .../proposal (internal/api/businesses.go): payload
// is present only when ready.
export type ProposalStatus = "generating" | "ready" | "failed"

// Current generation stage, present only while generating and only when the
// workflow's stage query answers (internal/workflows/generate_profile.go). An
// absent stage means "just started / unknown" — the UI treats it as step 1.
export type GenerationStage = "fetching_site" | "researching" | "drafting"

export interface ProposalStatusResponse {
  status: ProposalStatus
  stage?: GenerationStage
  payload?: ProposalPayload
}

export interface CreateBusinessRequest {
  name: string
  website: string
}

export interface BusinessResponse {
  id: string
  name: string
  status: string
}

export function createBusiness(
  req: CreateBusinessRequest
): Promise<BusinessResponse> {
  return apiPost<BusinessResponse>("/businesses", req)
}

export function getProposal(
  businessId: string
): Promise<ProposalStatusResponse> {
  return apiGet<ProposalStatusResponse>(`/businesses/${businessId}/proposal`)
}

export function regenProposal(
  businessId: string
): Promise<ProposalStatusResponse> {
  return apiPost<ProposalStatusResponse>(
    `/businesses/${businessId}/proposal/regen`
  )
}

// applyProposal posts the final edited payload to the apply endpoint (ONB-6):
// the server takes it verbatim, activates the business, inserts its prompts, and
// triggers the first monitoring run.
export function applyProposal(
  businessId: string,
  payload: ProposalPayload
): Promise<BusinessResponse> {
  return apiPost<BusinessResponse>(`/businesses/${businessId}/apply`, payload)
}

// useProposal polls while generation is running; a ready or failed proposal is
// terminal, so polling stops. Matches useRuns' data-driven refetchInterval.
export function useProposal(businessId: string | undefined) {
  return useQuery({
    queryKey: ["proposal", businessId],
    queryFn: () => getProposal(businessId!),
    enabled: businessId !== undefined,
    refetchInterval: (query) =>
      query.state.data?.status === "generating" ? 5000 : false,
  })
}

export function useRegenProposal(businessId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => regenProposal(businessId),
    onSuccess: (data) => {
      queryClient.setQueryData<ProposalStatusResponse>(
        ["proposal", businessId],
        data
      )
    },
  })
}

export function useApplyProposal(businessId: string) {
  return useMutation({
    mutationFn: (payload: ProposalPayload) =>
      applyProposal(businessId, payload),
  })
}
