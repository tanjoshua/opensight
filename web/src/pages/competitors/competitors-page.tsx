// Competitors section (design 06): triage the names discovered in your
// responses, then compare the ones you track and inspect their evidence.
import { useMutation } from "@connectrpc/connect-query"
import { Plus, Users } from "lucide-react"
import { useState } from "react"
import { useSearchParams } from "react-router"

import { useAccountNavigate } from "@/lib/account-path"

import { errorMessage } from "@/api/errors"
import {
  useAllCompetitors,
  useCurrentBusiness,
  useInvalidateCompetitorViews,
} from "@/api/hooks"
import { competitorStatusLabel } from "@/api/labels"
import {
  evidenceSelection,
  type EvidenceSelection,
} from "@/components/evidence-selection"
import { PageHeader } from "@/components/page-header"
import { ResponseDrawer } from "@/components/response-drawer"
import { SectionMessage } from "@/components/section-message"
import {
  AliasDecision,
  type Competitor,
  type CompetitorSelf,
} from "@/gen/opensight/v1/competitor_pb"
import { CompetitorStatus } from "@/gen/opensight/v1/common_pb"
import {
  addCompetitor,
  reviewSuggestedAlias,
  setCompetitorStatus,
} from "@/gen/opensight/v1/competitor-CompetitorService_connectquery"
import { formatPercent } from "@/lib/format"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { AddCompetitorDialog } from "./add-competitor-dialog"
import { DiscoveredSection } from "./discovered-section"
import { DismissedSection } from "./dismissed-section"
import type {
  ActionFeedback,
  AliasReview,
  MutableCompetitorStatus,
  StatusChange,
} from "./shared"
import { TrackedSection } from "./tracked-section"

export function CompetitorsPage() {
  const { business, isError, isReady } = useCurrentBusiness()
  const navigate = useAccountNavigate()
  const [searchParams] = useSearchParams()
  const focus = statusParam(searchParams.get("status"))
  const competitorsQuery = useAllCompetitors(business?.id)
  const invalidateCompetitorViews = useInvalidateCompetitorViews()
  const addCompetitorMutation = useMutation(addCompetitor, {
    onSuccess: invalidateCompetitorViews,
  })
  const setStatusMutation = useMutation(setCompetitorStatus, {
    onSuccess: invalidateCompetitorViews,
  })
  const reviewAliasMutation = useMutation(reviewSuggestedAlias, {
    onSettled: invalidateCompetitorViews,
  })
  const [selectedEvidence, setSelectedEvidence] = useState<EvidenceSelection>()
  const [addOpen, setAddOpen] = useState(false)
  const [statusFeedback, setStatusFeedback] = useState<ActionFeedback>()
  const [aliasFeedback, setAliasFeedback] = useState<ActionFeedback>()
  const openResult = (ids: string[], context?: string) =>
    setSelectedEvidence(
      evidenceSelection(
        ids,
        context ?? "Responses behind this competitor metric"
      )
    )

  if (isError || competitorsQuery.isError) {
    return (
      <SectionMessage
        icon={Users}
        title="Something went wrong"
        description="The competitors could not be loaded. Try reloading the page."
      />
    )
  }
  if (!isReady || !business || !competitorsQuery.data?.self) {
    return <CompetitorsSkeleton />
  }

  const { self, competitors } = competitorsQuery.data
  const discovered = competitors.filter(
    (competitor) => competitor.status === CompetitorStatus.DISCOVERED
  )
  const tracked = competitors.filter(
    (competitor) => competitor.status === CompetitorStatus.TRACKED
  )
  const dismissed = competitors.filter(
    (competitor) => competitor.status === CompetitorStatus.DISMISSED
  )
  const addError = addCompetitorMutation.isError
    ? errorMessage(
        addCompetitorMutation.error,
        "Could not add the competitor. Try again."
      )
    : undefined
  const pendingCompetitorID = setStatusMutation.isPending
    ? setStatusMutation.variables?.competitorId
    : undefined
  const pendingAlias =
    reviewAliasMutation.isPending && reviewAliasMutation.variables
      ? `${reviewAliasMutation.variables.competitorId}\u0000${reviewAliasMutation.variables.alias}`
      : undefined

  const changeStatus: StatusChange = (competitor, status, options) => {
    setStatusFeedback(undefined)
    setStatusMutation.mutate(
      { competitorId: competitor.id, status },
      {
        onSuccess: () => {
          const trackedNow = status === CompetitorStatus.TRACKED
          let previousStatus: MutableCompetitorStatus | undefined
          if (competitor.status === CompetitorStatus.TRACKED) {
            previousStatus = CompetitorStatus.TRACKED
          } else if (competitor.status === CompetitorStatus.DISMISSED) {
            previousStatus = CompetitorStatus.DISMISSED
          }
          setStatusFeedback({
            competitorId: competitor.id,
            message: options?.isUndo
              ? `${competitor.name} moved ${
                  trackedNow ? "back to tracked" : "to dismissed"
                }.`
              : trackedNow
                ? competitor.status === CompetitorStatus.DISMISSED
                  ? `${competitor.name} restored to tracked.`
                  : `${competitor.name} is now tracked.`
                : `${competitor.name} dismissed.`,
            tone: "success",
            undoStatus: options?.isUndo ? undefined : previousStatus,
          })
        },
        onError: (error) =>
          setStatusFeedback({
            competitorId: competitor.id,
            message: errorMessage(
              error,
              "Could not update this competitor. Try again."
            ),
            tone: "error",
          }),
      }
    )
  }
  const undoStatus = (
    competitor: Competitor,
    status: MutableCompetitorStatus
  ) => changeStatus(competitor, status, { isUndo: true })
  const changeAlias: AliasReview = (competitor, alias, decision) => {
    setAliasFeedback(undefined)
    reviewAliasMutation.mutate(
      { competitorId: competitor.id, alias, decision },
      {
        onSuccess: () =>
          setAliasFeedback({
            competitorId: competitor.id,
            message:
              decision === AliasDecision.APPROVE
                ? `“${alias}” approved as an alias.`
                : `“${alias}” rejected.`,
            tone: "success",
          }),
        onError: (error) =>
          setAliasFeedback({
            competitorId: competitor.id,
            message: errorMessage(
              error,
              "Could not review this alias. Try again."
            ),
            tone: "error",
          }),
      }
    )
  }
  const changeAddOpen = (open: boolean) => {
    setAddOpen(open)
    if (!open) addCompetitorMutation.reset()
  }

  return (
    <div className="flex min-w-0 flex-col gap-8">
      <div className="flex flex-col gap-4">
        <PageHeader
          title="Competitors"
          description="Compare who wins the questions you monitor, then inspect the evidence."
          actions={
            <Button onClick={() => changeAddOpen(true)}>
              <Plus data-icon="inline-start" />
              Add competitor
            </Button>
          }
        />
        <SelfBaseline self={self} onOpenResult={openResult} />
      </div>

      <AddCompetitorDialog
        open={addOpen}
        onOpenChange={changeAddOpen}
        submitting={addCompetitorMutation.isPending}
        errorMessage={addError}
        onSubmit={(input) =>
          addCompetitorMutation.mutate(
            { businessId: business.id, ...input },
            { onSuccess: () => setAddOpen(false) }
          )
        }
      />

      <DiscoveredSection
        competitors={discovered}
        self={self}
        focus={focus === CompetitorStatus.DISCOVERED}
        onOpenResult={openResult}
        onStatusChange={changeStatus}
        onUndoStatus={undoStatus}
        pendingCompetitorID={pendingCompetitorID}
        onReviewAlias={changeAlias}
        pendingAlias={pendingAlias}
        statusFeedback={statusFeedback}
        aliasFeedback={aliasFeedback}
      />
      <TrackedSection
        competitors={tracked}
        focus={focus === CompetitorStatus.TRACKED}
        onOpenResult={openResult}
        onSelectRun={(runID) => navigate(`/runs/${runID}`)}
        onStatusChange={changeStatus}
        onUndoStatus={undoStatus}
        pendingCompetitorID={pendingCompetitorID}
        onReviewAlias={changeAlias}
        pendingAlias={pendingAlias}
        statusFeedback={statusFeedback}
        aliasFeedback={aliasFeedback}
      />
      <DismissedSection
        competitors={dismissed}
        self={self}
        focus={focus === CompetitorStatus.DISMISSED}
        onOpenResult={openResult}
        onStatusChange={changeStatus}
        onUndoStatus={undoStatus}
        pendingCompetitorID={pendingCompetitorID}
        onReviewAlias={changeAlias}
        pendingAlias={pendingAlias}
        statusFeedback={statusFeedback}
        aliasFeedback={aliasFeedback}
      />

      <ResponseDrawer
        evidence={selectedEvidence}
        onOpenChange={(open) => {
          if (!open) setSelectedEvidence(undefined)
        }}
      />
    </div>
  )
}

function SelfBaseline({
  self,
  onOpenResult,
}: {
  self: CompetitorSelf
  onOpenResult: (ids: string[], context?: string) => void
}) {
  const disabled = self.resultIds.length === 0
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={() =>
        onOpenResult(self.resultIds, "Responses that mention your business")
      }
      title="View the responses behind your coverage"
      className="flex min-h-20 w-full items-center justify-between gap-4 rounded-xl border bg-card px-4 py-3 text-left enabled:cursor-pointer enabled:hover:bg-muted/50 disabled:opacity-70 sm:w-auto sm:min-w-64"
    >
      <div className="flex flex-col gap-1">
        <span className="text-xs text-muted-foreground">Your coverage</span>
        <span className="text-2xl font-semibold tabular-nums">
          {formatPercent(self.percent)}
        </span>
      </div>
      <span className="text-right text-xs text-muted-foreground">
        {self.mentioned} of {self.totalAnalyzed}
        <br />
        responses
      </span>
    </button>
  )
}

function CompetitorsSkeleton() {
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <div className="flex items-center justify-between">
        <Skeleton className="h-8 w-40" />
        <Skeleton className="h-14 w-48" />
      </div>
      <div className="flex flex-col gap-2">
        {Array.from({ length: 3 }, (_, index) => (
          <Skeleton key={index} className="h-14 w-full" />
        ))}
      </div>
      <Skeleton className="h-72 w-full" />
    </div>
  )
}

function statusParam(raw: string | null): CompetitorStatus | undefined {
  if (raw === competitorStatusLabel(CompetitorStatus.DISCOVERED)) {
    return CompetitorStatus.DISCOVERED
  }
  if (raw === competitorStatusLabel(CompetitorStatus.TRACKED)) {
    return CompetitorStatus.TRACKED
  }
  if (raw === competitorStatusLabel(CompetitorStatus.DISMISSED)) {
    return CompetitorStatus.DISMISSED
  }
  return undefined
}
