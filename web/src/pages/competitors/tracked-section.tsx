import { Fragment, type ReactNode, useState } from "react"

import { CompetitorStatus } from "@/gen/opensight/v1/common_pb"
import type { Competitor } from "@/gen/opensight/v1/competitor_pb"
import { formatPercent } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ActionNote, EmptyNote, SectionHeading, VsSelf } from "./section-parts"
import {
  averageRank,
  useScrollIntoView,
  type ActionFeedback,
  type CompetitorSectionProps,
  type MutableCompetitorStatus,
} from "./shared"
import { TrackedDetail } from "./tracked-detail"

export function TrackedSection({
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
                  <TableRow
                    data-state={selected ? "selected" : undefined}
                    onActivate={() => onSelect(competitor.id)}
                    activationRole="button"
                    aria-label={`View details for ${competitor.name}`}
                    aria-pressed={selected}
                  >
                    <TableCell className="max-w-60">
                      <button
                        type="button"
                        className="min-h-10 max-w-full truncate rounded font-medium hover:underline"
                        aria-pressed={selected}
                        onClick={(event) => {
                          event.stopPropagation()
                          onSelect(competitor.id)
                        }}
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
                        onClick={(event) => {
                          event.stopPropagation()
                          onSelect(competitor.id)
                        }}
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
      onClick={(event) => {
        event.stopPropagation()
        onOpenResult(
          competitor.resultIds,
          `Responses mentioning ${competitor.name}`
        )
      }}
    >
      {value}
    </button>
  )
}
