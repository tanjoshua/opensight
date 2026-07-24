import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiGet, apiPatch } from "./client"

export interface Location {
  address: string
  area: string
  city: string
  country: string
}

export interface BusinessPlan {
  slug: string
  prompt_limit: number
  run_interval: string
  platforms: string[]
}

export interface BusinessProfile {
  id: string
  status: string
  name: string
  website: string | null
  aliases: string[]
  category: string | null
  services: string[]
  location: Location
  plan: BusinessPlan
}

export type BusinessProfilePatch = Partial<
  Pick<
    BusinessProfile,
    "name" | "website" | "aliases" | "category" | "services" | "location"
  >
>

export function getBusiness(businessId: string): Promise<BusinessProfile> {
  return apiGet(`/businesses/${businessId}`)
}

export function patchBusiness(
  businessId: string,
  patch: BusinessProfilePatch
): Promise<BusinessProfile> {
  return apiPatch(`/businesses/${businessId}`, patch)
}

export function useBusiness(businessId: string | undefined) {
  return useQuery({
    queryKey: ["business", businessId],
    queryFn: () => getBusiness(businessId!),
    enabled: businessId !== undefined,
  })
}

export function usePatchBusiness(businessId: string | undefined) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (patch: BusinessProfilePatch) =>
      patchBusiness(businessId!, patch),
    onSuccess: (business) => {
      queryClient.setQueryData(["business", businessId], business)
      queryClient.invalidateQueries({ queryKey: ["me"] })
    },
  })
}
