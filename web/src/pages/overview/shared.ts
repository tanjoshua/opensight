import type { GetOverviewResponse } from "@/gen/opensight/v1/overview_pb"

// GetOverviewResponse's fields are already the flat Overview shape (no
// further response-wrapper unwrapping needed).
export type Overview = GetOverviewResponse

export type ExplorerMode = "snapshot" | "trend"
