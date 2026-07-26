import { useMutation } from "@connectrpc/connect-query"
import { ChevronDown, ChevronRight, Eye, Plus, Users } from "lucide-react"
import { Fragment, type ReactNode, useEffect, useRef, useState } from "react"
import { useNavigate, useSearchParams } from "react-router"
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts"

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
import {
  AliasDecision,
  type Competitor,
  type CompetitorPromptAppearance,
  type CompetitorSelf,
  type CompetitorTrendPoint,
} from "@/gen/opensight/v1/competitor_pb"
import { CompetitorStatus } from "@/gen/opensight/v1/common_pb"
import {
  addCompetitor,
  reviewSuggestedAlias,
  setCompetitorStatus,
} from "@/gen/opensight/v1/competitor-CompetitorService_connectquery"
import { Alert, AlertAction, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  ChartContainer,
  ChartTooltip,
  type ChartConfig,
} from "@/components/ui/chart"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { cn } from "@/lib/utils"

const chartConfig = {
  percent: { label: "Coverage", color: "var(--primary)" },
} satisfies ChartConfig

type MutableCompetitorStatus =
  typeof CompetitorStatus.TRACKED | typeof CompetitorStatus.DISMISSED

interface AddCompetitorInput {
  name: string
  aliases: string[]
  website: string
}

interface ActionFeedback {
  competitorId: string
  message: string
  tone: "success" | "error"
  undoStatus?: MutableCompetitorStatus
}

type StatusChange = (
  competitor: Competitor,
  status: MutableCompetitorStatus,
  options?: { isUndo?: boolean }
) => void

export function CompetitorsPage() {
  const { business, isError, isReady } = useCurrentBusiness()
  const navigate = useNavigate()
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
        title="Something went wrong"
        description="The competitors could not be loaded. Try reloading the page."
      />
    )
  }
  if (!isReady || !business || !competitorsQuery.data?.self) {
    return <ListSkeleton />
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
  const changeAlias = (
    competitor: Competitor,
    alias: string,
    decision: typeof AliasDecision.APPROVE | typeof AliasDecision.REJECT
  ) => {
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

interface CompetitorSectionProps {
  competitors: Competitor[]
  focus: boolean
  onOpenResult: (ids: string[], context?: string) => void
  onStatusChange: StatusChange
  onUndoStatus: (
    competitor: Competitor,
    status: MutableCompetitorStatus
  ) => void
  pendingCompetitorID?: string
  onReviewAlias: (
    competitor: Competitor,
    alias: string,
    decision: typeof AliasDecision.APPROVE | typeof AliasDecision.REJECT
  ) => void
  pendingAlias?: string
  statusFeedback?: ActionFeedback
  aliasFeedback?: ActionFeedback
}

function DiscoveredSection({
  competitors,
  self,
  focus,
  onOpenResult,
  onStatusChange,
  onUndoStatus,
  pendingCompetitorID,
  onReviewAlias,
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
                <SuggestedAliasReview
                  competitor={competitor}
                  onReview={onReviewAlias}
                  pendingAlias={pendingAlias}
                />
              }
              actions={
                <>
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

function CoverageRow({
  competitor,
  total,
  onOpenResult,
  actions,
  aliasReview,
  feedback,
  onUndoStatus,
}: {
  competitor: Competitor
  total: number
  onOpenResult: (ids: string[], context?: string) => void
  actions?: ReactNode
  aliasReview?: ReactNode
  feedback?: ActionFeedback
  onUndoStatus?: (status: MutableCompetitorStatus) => void
}) {
  const disabled = competitor.resultIds.length === 0
  return (
    <div className="flex min-w-0 flex-col gap-2 rounded-lg border bg-card px-3 py-3">
      <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="truncate font-medium">{competitor.name}</span>
          <button
            type="button"
            disabled={disabled}
            onClick={() =>
              onOpenResult(
                competitor.resultIds,
                `Responses mentioning ${competitor.name}`
              )
            }
            title="View responses behind this coverage"
            className="flex min-h-9 items-center gap-3 self-start rounded text-left text-sm text-muted-foreground enabled:cursor-pointer enabled:hover:text-foreground disabled:opacity-70"
          >
            <span className="tabular-nums">
              {competitor.mentioned} of {total} responses
            </span>
            <Badge variant="outline" className="tabular-nums">
              {formatPercent(competitor.mentionPercent)}
            </Badge>
          </button>
        </div>
        {actions && (
          <div className="flex shrink-0 items-center gap-2">{actions}</div>
        )}
      </div>
      {feedback && (
        <ActionNote
          feedback={feedback}
          onUndo={
            feedback.undoStatus && onUndoStatus
              ? () => onUndoStatus(feedback.undoStatus!)
              : undefined
          }
        />
      )}
      {aliasReview}
    </div>
  )
}

function TrackedSection({
  competitors,
  focus,
  onOpenResult,
  onSelectRun,
  onStatusChange,
  onUndoStatus,
  pendingCompetitorID,
  onReviewAlias,
  pendingAlias,
  statusFeedback,
  aliasFeedback,
}: CompetitorSectionProps & { onSelectRun: (runID: string) => void }) {
  const ref = useScrollIntoView<HTMLDivElement>(focus)
  const [selectedID, setSelectedID] = useState(competitors[0]?.id)
  const selected =
    competitors.find((competitor) => competitor.id === selectedID) ??
    competitors[0]

  return (
    <section ref={ref} className="flex min-w-0 flex-col gap-3">
      <SectionHeading
        title={`Tracked${competitors.length ? ` (${competitors.length})` : ""}`}
        description="Select one competitor to inspect its questions, evidence, aliases, and trend."
      />
      {competitors.length === 0 ? (
        <EmptyNote>
          No tracked competitors yet. Track a discovered competitor or add one
          manually.
        </EmptyNote>
      ) : (
        <>
          <TrackedComparison
            competitors={competitors}
            selectedID={selected?.id}
            onSelect={setSelectedID}
            onOpenResult={onOpenResult}
            feedback={statusFeedback}
            onUndoStatus={onUndoStatus}
          />
          {selected && (
            <TrackedDetail
              competitor={selected}
              onOpenResult={onOpenResult}
              onSelectRun={onSelectRun}
              onDismiss={() =>
                onStatusChange(selected, CompetitorStatus.DISMISSED)
              }
              statusPending={pendingCompetitorID === selected.id}
              onReviewAlias={onReviewAlias}
              pendingAlias={pendingAlias}
              aliasFeedback={
                aliasFeedback?.competitorId === selected.id
                  ? aliasFeedback
                  : undefined
              }
            />
          )}
        </>
      )}
    </section>
  )
}

function TrackedComparison({
  competitors,
  selectedID,
  onSelect,
  onOpenResult,
  feedback,
  onUndoStatus,
}: {
  competitors: Competitor[]
  selectedID?: string
  onSelect: (id: string) => void
  onOpenResult: (ids: string[], context?: string) => void
  feedback?: ActionFeedback
  onUndoStatus: (
    competitor: Competitor,
    status: MutableCompetitorStatus
  ) => void
}) {
  return (
    <>
      <div className="hidden overflow-hidden rounded-xl border bg-card md:block">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Competitor</TableHead>
              <TableHead className="text-right">Coverage</TableHead>
              <TableHead className="text-right">vs you</TableHead>
              <TableHead className="text-right">Mentions</TableHead>
              <TableHead className="text-right">Avg. rank</TableHead>
              <TableHead className="w-24 text-right">
                <span className="sr-only">Details</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {competitors.map((competitor) => {
              const selected = competitor.id === selectedID
              const rowFeedback =
                feedback?.competitorId === competitor.id ? feedback : undefined
              return (
                <Fragment key={competitor.id}>
                  <TableRow data-state={selected ? "selected" : undefined}>
                    <TableCell className="max-w-60">
                      <button
                        type="button"
                        className="min-h-10 max-w-full truncate rounded font-medium hover:underline"
                        aria-pressed={selected}
                        onClick={() => onSelect(competitor.id)}
                      >
                        {competitor.name}
                      </button>
                    </TableCell>
                    <TableCell className="text-right">
                      <MetricLink
                        competitor={competitor}
                        value={formatPercent(competitor.mentionPercent)}
                        onOpenResult={onOpenResult}
                      />
                    </TableCell>
                    <TableCell className="text-right">
                      <VsSelf vsSelf={competitor.vsSelf} />
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {competitor.totalMentions}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {averageRank(competitor)}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        variant={selected ? "secondary" : "ghost"}
                        aria-label={`View details for ${competitor.name}`}
                        onClick={() => onSelect(competitor.id)}
                      >
                        {selected ? "Selected" : "View"}
                      </Button>
                    </TableCell>
                  </TableRow>
                  {rowFeedback && (
                    <TableRow>
                      <TableCell colSpan={6} className="whitespace-normal">
                        <ActionNote
                          feedback={rowFeedback}
                          onUndo={
                            rowFeedback.undoStatus
                              ? () =>
                                  onUndoStatus(
                                    competitor,
                                    rowFeedback.undoStatus!
                                  )
                              : undefined
                          }
                        />
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              )
            })}
          </TableBody>
        </Table>
      </div>

      <div className="flex flex-col gap-2 md:hidden">
        {competitors.map((competitor) => {
          const selected = competitor.id === selectedID
          const rowFeedback =
            feedback?.competitorId === competitor.id ? feedback : undefined
          return (
            <Card
              key={competitor.id}
              className={cn("gap-3 py-4", selected && "ring-1 ring-primary")}
            >
              <CardHeader className="gap-2 px-4">
                <div className="flex min-w-0 items-center justify-between gap-3">
                  <CardTitle className="min-w-0 truncate text-base">
                    {competitor.name}
                  </CardTitle>
                  <Button
                    size="sm"
                    variant={selected ? "secondary" : "outline"}
                    className="min-h-10"
                    aria-pressed={selected}
                    onClick={() => onSelect(competitor.id)}
                  >
                    {selected ? "Selected" : "View"}
                  </Button>
                </div>
              </CardHeader>
              <CardContent className="grid grid-cols-2 gap-3 px-4">
                <MobileComparisonStat label="Coverage">
                  <MetricLink
                    competitor={competitor}
                    value={formatPercent(competitor.mentionPercent)}
                    onOpenResult={onOpenResult}
                  />
                </MobileComparisonStat>
                <MobileComparisonStat label="vs you">
                  <VsSelf vsSelf={competitor.vsSelf} />
                </MobileComparisonStat>
                <MobileComparisonStat label="Mentions">
                  <span className="font-medium tabular-nums">
                    {competitor.totalMentions}
                  </span>
                </MobileComparisonStat>
                <MobileComparisonStat label="Avg. rank">
                  <span className="font-medium tabular-nums">
                    {averageRank(competitor)}
                  </span>
                </MobileComparisonStat>
                {rowFeedback && (
                  <div className="col-span-2">
                    <ActionNote
                      feedback={rowFeedback}
                      onUndo={
                        rowFeedback.undoStatus
                          ? () =>
                              onUndoStatus(competitor, rowFeedback.undoStatus!)
                          : undefined
                      }
                    />
                  </div>
                )}
              </CardContent>
            </Card>
          )
        })}
      </div>
    </>
  )
}

function MobileComparisonStat({
  label,
  children,
}: {
  label: string
  children: ReactNode
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="text-xs text-muted-foreground">{label}</span>
      {children}
    </div>
  )
}

function MetricLink({
  competitor,
  value,
  onOpenResult,
}: {
  competitor: Competitor
  value: string
  onOpenResult: (ids: string[], context?: string) => void
}) {
  return (
    <button
      type="button"
      disabled={competitor.resultIds.length === 0}
      className="min-h-9 rounded font-medium tabular-nums enabled:hover:underline disabled:text-muted-foreground"
      title="View responses behind this metric"
      onClick={() =>
        onOpenResult(
          competitor.resultIds,
          `Responses mentioning ${competitor.name}`
        )
      }
    >
      {value}
    </button>
  )
}

function TrackedDetail({
  competitor,
  onOpenResult,
  onSelectRun,
  onDismiss,
  statusPending,
  onReviewAlias,
  pendingAlias,
  aliasFeedback,
}: {
  competitor: Competitor
  onOpenResult: (ids: string[], context?: string) => void
  onSelectRun: (runID: string) => void
  onDismiss: () => void
  statusPending: boolean
  onReviewAlias: (
    competitor: Competitor,
    alias: string,
    decision: typeof AliasDecision.APPROVE | typeof AliasDecision.REJECT
  ) => void
  pendingAlias?: string
  aliasFeedback?: ActionFeedback
}) {
  const openOwn = () =>
    onOpenResult(
      competitor.resultIds,
      `Responses mentioning ${competitor.name}`
    )
  const hasEvidence = competitor.resultIds.length > 0
  return (
    <Card className="min-w-0 overflow-hidden">
      <CardHeader className="gap-1">
        <CardTitle>{competitor.name}</CardTitle>
        <CardDescription>
          {hasEvidence
            ? `Mentioned in ${competitor.mentioned} responses`
            : "Tracked now; metrics will fill in when a future response mentions this competitor."}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex min-w-0 flex-col gap-6">
        <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <DetailStat
            label="Coverage"
            value={formatPercent(competitor.mentionPercent)}
            onClick={openOwn}
            disabled={!hasEvidence}
          />
          <div className="flex flex-col gap-1">
            <span className="text-xs text-muted-foreground">vs you</span>
            <VsSelf vsSelf={competitor.vsSelf} />
          </div>
          <DetailStat
            label="Total mentions"
            value={String(competitor.totalMentions)}
            onClick={openOwn}
            disabled={!hasEvidence}
          />
          <DetailStat
            label="Avg. rank"
            value={averageRank(competitor)}
            onClick={openOwn}
            disabled={!hasEvidence}
          />
        </div>

        <Separator />

        <div className="grid min-w-0 gap-6 lg:grid-cols-[minmax(0,1.4fr)_minmax(16rem,0.6fr)]">
          <div className="flex min-w-0 flex-col gap-2">
            <h3 className="text-sm font-medium">Weekly coverage</h3>
            <TrendChart trend={competitor.trend} onSelectRun={onSelectRun} />
          </div>
          <div className="flex min-w-0 flex-col gap-5">
            <Aliases competitor={competitor} />
            <SuggestedAliasReview
              competitor={competitor}
              onReview={onReviewAlias}
              pendingAlias={pendingAlias}
            />
            {aliasFeedback && <ActionNote feedback={aliasFeedback} />}
            <EvidenceSummary
              competitor={competitor}
              onOpenResult={onOpenResult}
            />
          </div>
        </div>

        <PromptAppearances
          appearances={competitor.perPrompt}
          competitorName={competitor.name}
          onOpenResult={onOpenResult}
        />
      </CardContent>
      <CardFooter className="justify-end">
        <Button
          size="sm"
          variant="outline"
          className="min-h-10 sm:min-h-0"
          aria-label={`Dismiss ${competitor.name}`}
          disabled={statusPending}
          onClick={onDismiss}
        >
          Dismiss competitor
        </Button>
      </CardFooter>
    </Card>
  )
}

function DetailStat({
  label,
  value,
  onClick,
  disabled,
}: {
  label: string
  value: string
  onClick: () => void
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title="View responses behind this metric"
      className="flex min-h-14 flex-col items-start gap-1 rounded text-left enabled:cursor-pointer enabled:hover:opacity-70 disabled:opacity-70"
    >
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-2xl font-semibold tabular-nums">{value}</span>
    </button>
  )
}

function VsSelf({ vsSelf }: { vsSelf: number }) {
  const rounded = Math.round(vsSelf * 10) / 10
  const sign = rounded > 0 ? "+" : ""
  return (
    <Badge
      variant={rounded > 0 ? "destructive" : "secondary"}
      className="w-fit text-sm"
    >
      {sign}
      {rounded.toFixed(1)} pts
    </Badge>
  )
}

function Aliases({ competitor }: { competitor: Competitor }) {
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <h3 className="text-sm font-medium">Approved aliases</h3>
      {competitor.aliases.length === 0 ? (
        <p className="text-xs text-muted-foreground">No aliases added.</p>
      ) : (
        <div className="flex flex-wrap gap-1.5">
          {competitor.aliases.map((alias) => (
            <Badge
              key={alias}
              variant="outline"
              className="max-w-full truncate"
            >
              {alias}
            </Badge>
          ))}
        </div>
      )}
    </div>
  )
}

function EvidenceSummary({
  competitor,
  onOpenResult,
}: {
  competitor: Competitor
  onOpenResult: (ids: string[], context?: string) => void
}) {
  const count = competitor.resultIds.length
  return (
    <div className="flex flex-col gap-2">
      <h3 className="text-sm font-medium">Evidence</h3>
      {count === 0 ? (
        <p className="text-xs text-muted-foreground">
          No mention evidence yet.
        </p>
      ) : (
        <Button
          size="sm"
          variant="outline"
          className="min-h-10 self-start"
          onClick={() =>
            onOpenResult(
              competitor.resultIds,
              `Responses mentioning ${competitor.name}`
            )
          }
        >
          <Eye data-icon="inline-start" />
          View {count} {count === 1 ? "response" : "responses"}
        </Button>
      )}
    </div>
  )
}

function PromptAppearances({
  appearances,
  competitorName,
  onOpenResult,
}: {
  appearances: CompetitorPromptAppearance[]
  competitorName: string
  onOpenResult: (ids: string[], context?: string) => void
}) {
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <h3 className="text-sm font-medium">Question appearances</h3>
      {appearances.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          This competitor has not appeared for a monitored question yet.
        </p>
      ) : (
        <div className="max-h-64 overflow-y-auto rounded-lg border">
          {appearances.map((appearance) => (
            <button
              key={appearance.promptId}
              type="button"
              onClick={() =>
                onOpenResult(
                  appearance.resultIds,
                  `Responses mentioning ${competitorName} for “${appearance.promptText}”`
                )
              }
              title="View responses for this question"
              className="flex min-h-12 w-full items-start justify-between gap-3 border-b px-3 py-2.5 text-left text-sm last:border-b-0 hover:bg-muted/50"
            >
              <span className="line-clamp-2 min-w-0">
                {appearance.promptText}
              </span>
              <Badge variant="outline" className="shrink-0 tabular-nums">
                {appearance.resultIds.length}
              </Badge>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function SuggestedAliasReview({
  competitor,
  onReview,
  pendingAlias,
}: {
  competitor: Competitor
  onReview: (
    competitor: Competitor,
    alias: string,
    decision: typeof AliasDecision.APPROVE | typeof AliasDecision.REJECT
  ) => void
  pendingAlias?: string
}) {
  if (competitor.suggestedAliases.length === 0) return null
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <h3 className="text-sm font-medium">Suggested aliases</h3>
      <div className="flex flex-col gap-2">
        {competitor.suggestedAliases.map((alias) => {
          const pending = pendingAlias === `${competitor.id}\u0000${alias}`
          return (
            <div
              key={alias}
              className="flex min-w-0 flex-col gap-2 rounded-lg bg-muted/50 px-3 py-2 sm:flex-row sm:items-center sm:justify-between"
            >
              <Badge
                variant="outline"
                className="max-w-full self-start truncate"
              >
                {alias}
              </Badge>
              <div className="flex items-center gap-2">
                <Button
                  size="sm"
                  className="min-h-10 flex-1 sm:min-h-0 sm:flex-none"
                  aria-label={`Approve ${alias} as an alias for ${competitor.name}`}
                  disabled={pending}
                  onClick={() =>
                    onReview(competitor, alias, AliasDecision.APPROVE)
                  }
                >
                  Approve
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  className="min-h-10 flex-1 sm:min-h-0 sm:flex-none"
                  aria-label={`Reject ${alias} as an alias for ${competitor.name}`}
                  disabled={pending}
                  onClick={() =>
                    onReview(competitor, alias, AliasDecision.REJECT)
                  }
                >
                  Reject
                </Button>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

function TrendChart({
  trend,
  onSelectRun,
}: {
  trend: CompetitorTrendPoint[]
  onSelectRun: (runID: string) => void
}) {
  if (trend.length === 0) {
    return (
      <EmptyNote>
        No analyzed runs yet. Zero-history manual competitors stay tracked here.
      </EmptyNote>
    )
  }
  if (trend.length === 1) {
    const point = trend[0]
    return (
      <button
        type="button"
        onClick={() => onSelectRun(point.runId)}
        className="flex min-h-12 cursor-pointer items-center gap-2 self-start rounded-lg border px-3 py-2 text-left text-xs hover:bg-muted/50"
      >
        <span className="size-2.5 rounded-full bg-primary" />
        <span>
          {shortDate(dateMs(point.scheduledFor))} ·{" "}
          {formatPercent(point.percent)} · the trend starts after the next run
        </span>
      </button>
    )
  }

  const data = trend.map((point) => ({ x: dateMs(point.scheduledFor), point }))
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <ChartContainer config={chartConfig} className="h-56 w-full min-w-0">
        <LineChart
          accessibilityLayer
          data={data}
          margin={{ left: 4, right: 12, top: 8 }}
          onClick={(state) => {
            const point = (
              state as unknown as {
                activePayload?: { payload: { point: CompetitorTrendPoint } }[]
              }
            ).activePayload?.[0]?.payload.point
            if (point) onSelectRun(point.runId)
          }}
        >
          <CartesianGrid vertical={false} />
          <XAxis
            dataKey="x"
            type="number"
            scale="time"
            domain={["dataMin", "dataMax"]}
            ticks={data.map((datum) => datum.x)}
            tickLine={false}
            axisLine={false}
            tickMargin={8}
            tickFormatter={(ms: number) => shortDate(ms)}
          />
          <YAxis
            domain={[0, 100]}
            width={36}
            tickLine={false}
            axisLine={false}
            tickFormatter={(value: number) => `${value}%`}
          />
          <ChartTooltip cursor content={<TrendTooltip />} />
          <Line
            dataKey="point.percent"
            name="percent"
            type="monotone"
            stroke="var(--color-percent)"
            strokeWidth={2}
            dot={{ r: 3 }}
            activeDot={{ r: 5 }}
          />
        </LineChart>
      </ChartContainer>
      <nav
        className="flex gap-2 overflow-x-auto pb-1"
        aria-label="Open competitor evidence by monitoring run"
      >
        {trend.map((point) => (
          <Button
            key={point.runId}
            type="button"
            size="xs"
            variant="outline"
            className="min-h-11 shrink-0"
            onClick={() => onSelectRun(point.runId)}
          >
            {shortDate(dateMs(point.scheduledFor))} ·{" "}
            {formatPercent(point.percent)}
          </Button>
        ))}
      </nav>
      <p className="text-xs text-muted-foreground">
        Select a dated run to open its evidence.
      </p>
    </div>
  )
}

function TrendTooltip({
  active,
  payload,
}: {
  active?: boolean
  payload?: { payload: { point: CompetitorTrendPoint } }[]
}) {
  if (!active || !payload?.length) return null
  const point = payload[0].payload.point
  return (
    <div className="rounded-lg border bg-background px-3 py-2 text-xs shadow-md">
      <div className="font-medium">{shortDate(dateMs(point.scheduledFor))}</div>
      <div className="text-muted-foreground">
        {formatPercent(point.percent)} · mentioned in {point.mentioned} of{" "}
        {point.analyzed}
      </div>
    </div>
  )
}

function DismissedSection({
  competitors,
  self,
  focus,
  onOpenResult,
  onStatusChange,
  onUndoStatus,
  pendingCompetitorID,
  onReviewAlias,
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
                <SuggestedAliasReview
                  competitor={competitor}
                  onReview={onReviewAlias}
                  pendingAlias={pendingAlias}
                />
              }
              actions={
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
              }
            />
          ))}
        </div>
      )}
    </section>
  )
}

function ActionNote({
  feedback,
  onUndo,
}: {
  feedback: ActionFeedback
  onUndo?: () => void
}) {
  return (
    <Alert
      variant={feedback.tone === "error" ? "destructive" : "default"}
      className="rounded-lg py-2"
    >
      <AlertDescription>{feedback.message}</AlertDescription>
      {onUndo && (
        <AlertAction>
          <Button size="xs" variant="outline" onClick={onUndo}>
            Undo
          </Button>
        </AlertAction>
      )}
    </Alert>
  )
}

function AddCompetitorDialog({
  open,
  onOpenChange,
  submitting,
  errorMessage: addErrorMessage,
  onSubmit,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  submitting: boolean
  errorMessage?: string
  onSubmit: (input: AddCompetitorInput) => void
}) {
  const [name, setName] = useState("")
  const [aliases, setAliases] = useState("")
  const [website, setWebsite] = useState("")
  const [wasOpen, setWasOpen] = useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) {
      setName("")
      setAliases("")
      setWebsite("")
    }
  }

  const trimmedName = name.trim()
  const canSubmit = trimmedName !== "" && !submitting
  const submit = () => {
    if (!canSubmit) return
    onSubmit({
      name: trimmedName,
      aliases: aliases
        .split(",")
        .map((alias) => alias.trim())
        .filter(Boolean),
      website: website.trim(),
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add competitor</DialogTitle>
          <DialogDescription>
            Manually added competitors start in your tracked list. Their history
            fills in when future responses mention them.
          </DialogDescription>
        </DialogHeader>

        <FieldGroup className="gap-4">
          <Field>
            <FieldLabel htmlFor="competitor-name">Name</FieldLabel>
            <Input
              id="competitor-name"
              value={name}
              autoFocus
              required
              placeholder="e.g. Rival Clinic"
              onChange={(event) => setName(event.currentTarget.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") submit()
              }}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="competitor-aliases">Aliases</FieldLabel>
            <Input
              id="competitor-aliases"
              value={aliases}
              placeholder="Rival Health, Rival Medical"
              onChange={(event) => setAliases(event.currentTarget.value)}
            />
            <FieldDescription>Optional, separated by commas.</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="competitor-website">Website</FieldLabel>
            <Input
              id="competitor-website"
              type="url"
              value={website}
              placeholder="https://example.com"
              onChange={(event) => setWebsite(event.currentTarget.value)}
            />
            <FieldDescription>Optional.</FieldDescription>
          </Field>
        </FieldGroup>

        {addErrorMessage && <FieldError>{addErrorMessage}</FieldError>}

        <DialogFooter>
          <Button
            variant="outline"
            disabled={submitting}
            onClick={() => onOpenChange(false)}
          >
            Cancel
          </Button>
          <Button disabled={!canSubmit} onClick={submit}>
            Add competitor
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function SectionHeading({
  title,
  description,
}: {
  title: string
  description: string
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <h2 className="font-heading text-base font-medium">{title}</h2>
      <p className="text-sm text-muted-foreground">{description}</p>
    </div>
  )
}

function EmptyNote({ children }: { children: ReactNode }) {
  return (
    <p className="rounded-lg border border-dashed px-3 py-4 text-sm text-muted-foreground">
      {children}
    </p>
  )
}

function useScrollIntoView<T extends HTMLElement>(focus: boolean) {
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

function SectionMessage({
  title,
  description,
}: {
  title: string
  description: string
}) {
  return (
    <Empty className="border">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <Users />
        </EmptyMedia>
        <EmptyTitle>{title}</EmptyTitle>
        <EmptyDescription>{description}</EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}

function ListSkeleton() {
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

function feedbackFor(
  competitorId: string,
  ...feedback: (ActionFeedback | undefined)[]
) {
  return feedback.find((item) => item?.competitorId === competitorId)
}

function averageRank(competitor: Competitor) {
  return competitor.resultIds.length > 0
    ? `#${(competitor.avgOrder + 1).toFixed(1)}`
    : "—"
}

function formatPercent(value: number): string {
  return `${(Math.round(value * 10) / 10).toFixed(1)}%`
}

function dateMs(scheduledFor: string): number {
  return new Date(`${scheduledFor}T00:00:00`).getTime()
}

function shortDate(ms: number): string {
  return new Date(ms).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  })
}
