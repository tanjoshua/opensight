import { Eye } from "lucide-react"

import type {
  Competitor,
  CompetitorPromptAppearance,
} from "@/gen/opensight/v1/competitor_pb"
import { formatPercent } from "@/lib/format"
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
import { Separator } from "@/components/ui/separator"
import { ActionNote, SuggestedAliasReview, VsSelf } from "./section-parts"
import { averageRank, type ActionFeedback, type AliasReview } from "./shared"
import { TrendChart } from "./trend-chart"

export function TrackedDetail({
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
  onReviewAlias: AliasReview
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
