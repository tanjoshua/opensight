import { ChevronDown, ChevronRight } from "lucide-react"
import { useState } from "react"

import { CompetitorStatus } from "@/gen/opensight/v1/common_pb"
import type { CompetitorSelf } from "@/gen/opensight/v1/competitor_pb"
import { Button } from "@/components/ui/button"
import { ApprovedAliases } from "./approved-aliases"
import {
  ActionNote,
  ClaimSelfButton,
  CoverageRow,
  SuggestedAliasReview,
} from "./section-parts"
import {
  feedbackFor,
  useScrollIntoView,
  type CompetitorSectionProps,
} from "./shared"

export function DismissedSection({
  competitors,
  self,
  focus,
  onOpenResult,
  onStatusChange,
  onUndoStatus,
  pendingCompetitorID,
  onReviewAlias,
  onClaimSelf,
  pendingAlias,
  statusFeedback,
  aliasFeedback,
}: CompetitorSectionProps & { self: CompetitorSelf }) {
  const [open, setOpen] = useState(focus)
  const ref = useScrollIntoView<HTMLDivElement>(focus)
  const sectionFeedback = competitors.some(
    (competitor) => competitor.id === statusFeedback?.competitorId
  )
    ? statusFeedback
    : undefined
  const feedbackCompetitor = sectionFeedback
    ? competitors.find(
        (competitor) => competitor.id === sectionFeedback.competitorId
      )
    : undefined

  if (competitors.length === 0) return null
  return (
    <section ref={ref} className="flex min-w-0 flex-col gap-3">
      <button
        type="button"
        onClick={() => setOpen((value) => !value)}
        aria-expanded={open}
        aria-controls="dismissed-competitors-panel"
        className="flex min-h-11 cursor-pointer items-center gap-1.5 self-start rounded text-left"
      >
        {open ? (
          <ChevronDown className="size-4 text-muted-foreground" />
        ) : (
          <ChevronRight className="size-4 text-muted-foreground" />
        )}
        <span className="font-heading text-sm font-medium">
          Dismissed ({competitors.length})
        </span>
      </button>
      {sectionFeedback && !open && feedbackCompetitor && (
        <ActionNote
          feedback={sectionFeedback}
          onUndo={
            sectionFeedback.undoStatus
              ? () =>
                  onUndoStatus(feedbackCompetitor, sectionFeedback.undoStatus!)
              : undefined
          }
        />
      )}
      {open && (
        <div id="dismissed-competitors-panel" className="flex flex-col gap-2">
          <p className="text-xs text-muted-foreground">
            Dismissed competitors keep their full history and can be restored.
          </p>
          {competitors.map((competitor) => (
            <CoverageRow
              key={competitor.id}
              competitor={competitor}
              total={self.totalAnalyzed}
              onOpenResult={onOpenResult}
              feedback={feedbackFor(
                competitor.id,
                statusFeedback,
                aliasFeedback
              )}
              onUndoStatus={(status) => onUndoStatus(competitor, status)}
              aliasReview={
                <>
                  <ApprovedAliases competitor={competitor} />
                  <SuggestedAliasReview
                    competitor={competitor}
                    onReview={onReviewAlias}
                    pendingAlias={pendingAlias}
                  />
                </>
              }
              actions={
                <>
                  <ClaimSelfButton
                    competitor={competitor}
                    onClaimSelf={onClaimSelf}
                  />
                  <Button
                    size="sm"
                    className="min-h-10 sm:min-h-0"
                    aria-label={`Restore ${competitor.name} to tracked`}
                    disabled={pendingCompetitorID === competitor.id}
                    onClick={() =>
                      onStatusChange(competitor, CompetitorStatus.TRACKED)
                    }
                  >
                    Restore
                  </Button>
                </>
              }
            />
          ))}
        </div>
      )}
    </section>
  )
}
