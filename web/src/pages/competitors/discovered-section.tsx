import { CompetitorStatus } from "@/gen/opensight/v1/common_pb"
import type { CompetitorSelf } from "@/gen/opensight/v1/competitor_pb"
import { Button } from "@/components/ui/button"
import { ApprovedAliases } from "./approved-aliases"
import {
  ClaimSelfButton,
  CoverageRow,
  EmptyNote,
  SectionHeading,
  SuggestedAliasReview,
} from "./section-parts"
import {
  feedbackFor,
  useScrollIntoView,
  type CompetitorSectionProps,
} from "./shared"

export function DiscoveredSection({
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
  const ref = useScrollIntoView<HTMLDivElement>(focus)
  return (
    <section ref={ref} className="flex min-w-0 flex-col gap-3">
      <SectionHeading
        title={`Discovered${competitors.length ? ` (${competitors.length})` : ""}`}
        description="New names found in your responses, ranked by coverage."
      />
      {competitors.length === 0 ? (
        <EmptyNote>No discovered competitors — all triaged.</EmptyNote>
      ) : (
        <div className="flex flex-col gap-2">
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
                    aria-label={`Track ${competitor.name}`}
                    disabled={pendingCompetitorID === competitor.id}
                    onClick={() =>
                      onStatusChange(competitor, CompetitorStatus.TRACKED)
                    }
                  >
                    Track
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    className="min-h-10 sm:min-h-0"
                    aria-label={`Dismiss ${competitor.name}`}
                    disabled={pendingCompetitorID === competitor.id}
                    onClick={() =>
                      onStatusChange(competitor, CompetitorStatus.DISMISSED)
                    }
                  >
                    Dismiss
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
