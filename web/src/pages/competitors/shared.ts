import { useEffect, useRef } from "react"

import { CompetitorStatus } from "@/gen/opensight/v1/common_pb"
import type {
  AliasDecision,
  Competitor,
} from "@/gen/opensight/v1/competitor_pb"

export type MutableCompetitorStatus =
  typeof CompetitorStatus.TRACKED | typeof CompetitorStatus.DISMISSED

export interface ActionFeedback {
  competitorId: string
  message: string
  tone: "success" | "error"
  undoStatus?: MutableCompetitorStatus
}

export type StatusChange = (
  competitor: Competitor,
  status: MutableCompetitorStatus,
  options?: { isUndo?: boolean }
) => void

export type AliasReview = (
  competitor: Competitor,
  alias: string,
  decision: typeof AliasDecision.APPROVE | typeof AliasDecision.REJECT
) => void

// The props every competitor section (discovered, tracked, dismissed) takes:
// its slice of the list plus the shared mutation handlers and their feedback.
export interface CompetitorSectionProps {
  competitors: Competitor[]
  focus: boolean
  onOpenResult: (ids: string[], context?: string) => void
  onStatusChange: StatusChange
  onUndoStatus: (
    competitor: Competitor,
    status: MutableCompetitorStatus
  ) => void
  pendingCompetitorID?: string
  onReviewAlias: AliasReview
  pendingAlias?: string
  statusFeedback?: ActionFeedback
  aliasFeedback?: ActionFeedback
}

// Scrolls a section into view once, when it is the ?status= deep link target.
export function useScrollIntoView<T extends HTMLElement>(focus: boolean) {
  const ref = useRef<T>(null)
  const done = useRef(false)
  useEffect(() => {
    if (focus && !done.current && ref.current) {
      done.current = true
      ref.current.scrollIntoView({ behavior: "smooth", block: "start" })
    }
  }, [focus])
  return ref
}

export function feedbackFor(
  competitorId: string,
  ...feedback: (ActionFeedback | undefined)[]
) {
  return feedback.find((item) => item?.competitorId === competitorId)
}

export function averageRank(competitor: Competitor) {
  return competitor.resultIds.length > 0
    ? `#${(competitor.avgOrder + 1).toFixed(1)}`
    : "—"
}
