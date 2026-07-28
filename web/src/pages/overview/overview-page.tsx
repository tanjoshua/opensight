// Overview section (INS-1, design 06): one page answering "how visible am I and
// what changed". Headline visibility stat + weekly trend line with prompt-set
// change markers, then three compact panels (themes, cited domains, competitors).
// Every number is a door — stats open the Response drawer via their result_ids.
// The adaptive visibility explorer inspects historical runs without changing
// the independently scoped evidence elsewhere in the Brief.
import { skipToken, useQuery } from "@connectrpc/connect-query"
import {
  ArrowDownRight,
  ArrowRight,
  ArrowUpRight,
  LayoutDashboard,
  TriangleAlert,
} from "lucide-react"
import { useState } from "react"
import { Link, useNavigate } from "react-router"
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  LabelList,
  Line,
  LineChart,
  ReferenceLine,
  XAxis,
  YAxis,
} from "recharts"

import { pollWhileRunning, useCurrentBusiness } from "@/api/hooks"
import { CompetitorStatus, RunStatus } from "@/gen/opensight/v1/common_pb"
import type {
  CompetitorSummary,
  DomainStat,
  GetOverviewResponse,
  PromptChange,
  VisibilityPoint,
} from "@/gen/opensight/v1/overview_pb"
import { getOverview } from "@/gen/opensight/v1/overview-OverviewService_connectquery"
import type { PromptSummary } from "@/gen/opensight/v1/prompt_pb"
import { listPrompts } from "@/gen/opensight/v1/prompt-PromptService_connectquery"
import { CitationSourcesDrilldown } from "@/components/citation-sources-drilldown"
import { PageHeader } from "@/components/page-header"
import { ResponseDrawer } from "@/components/response-drawer"
import {
  evidenceSelection,
  type EvidenceSelection,
} from "@/components/evidence-selection"
import { RunStageStrip } from "@/components/run-stage-strip"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
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
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

const chartConfig = {
  you: { label: "You", color: "var(--chart-1)" },
} satisfies ChartConfig

// The Overview trend plots "You" against a few competitors, one line each,
// identified by color: --chart-1 (you) through --chart-4 (up to 3
// competitors), a fixed-order categorical set validated colorblind-safe as a
// line-chart adjacent-pair palette (dataviz skill). "You" stays visually
// dominant via line/dot weight, not color alone — a legend still names every
// line since two of the four slots (aqua, yellow) sit under 3:1 contrast on a
// light card. Cap to the top few so the chart stays legible.
const MAX_TREND_COMPETITOR_LINES = 2
const MAX_SNAPSHOT_COMPETITORS = 3
const COMPETITOR_COLORS = ["var(--chart-2)", "var(--chart-3)", "var(--chart-4)"]
const BRIEF_LIST_LIMIT = 5

// GetOverviewResponse's fields are already the flat Overview shape (no
// further response-wrapper unwrapping needed).
type Overview = GetOverviewResponse

export function OverviewPage() {
  const { business, isError, isReady } = useCurrentBusiness()
  const navigate = useNavigate()
  // Overview polls off its own latest_run rather than the runs list, but with
  // the shared poll-while-running cadence (first-run-in-progress, design 06).
  const overview = useQuery(
    getOverview,
    business === undefined ? skipToken : { businessId: business.id },
    {
      refetchInterval: pollWhileRunning(
        (data: Overview) => data.latestRun?.status === RunStatus.RUNNING
      ),
    }
  )
  const prompts = useQuery(
    listPrompts,
    business === undefined ? skipToken : { businessId: business.id }
  )
  // Opening the drawer is the shared "every number is a door" action: retain
  // the complete ordered evidence set behind a metric.
  const [selectedEvidence, setSelectedEvidence] = useState<EvidenceSelection>()
  const [selectedCitationDomain, setSelectedCitationDomain] = useState<string>()
  const [explorerModeOverride, setExplorerModeOverride] =
    useState<ExplorerMode>()
  const openResult = (ids: string[], context?: string) =>
    setSelectedEvidence(
      evidenceSelection(ids, context ?? "Responses behind this overview metric")
    )

  if (isError) {
    return (
      <SectionMessage
        title="Something went wrong"
        description="The overview could not be loaded. Try reloading the page."
      />
    )
  }
  if (!isReady) {
    return <OverviewSkeleton />
  }
  if (!business) {
    return <OverviewSkeleton />
  }
  if (overview.isError || prompts.isError) {
    return (
      <OverviewFrame>
        <SectionMessage
          title="Something went wrong"
          description="The overview could not be loaded. Try reloading the page."
        />
      </OverviewFrame>
    )
  }
  if (!overview.data || !prompts.data) {
    return (
      <OverviewFrame>
        <OverviewSkeleton />
      </OverviewFrame>
    )
  }

  const data = overview.data
  const promptSummaries = prompts.data.prompts
  const trendLength = data.visibility?.trend.length ?? 0
  const explorerMode: ExplorerMode =
    trendLength < 2 ? "snapshot" : (explorerModeOverride ?? "trend")

  // No analyzed history yet: either no run has happened, the first run is still
  // in flight, or results are awaiting analysis (design 06 degraded states).
  if (trendLength === 0) {
    return (
      <OverviewFrame>
        <NoDataState overview={data} />
      </OverviewFrame>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <OverviewHeader />

      <PartialRunBanner overview={data} />

      <VisibilityCard
        overview={data}
        mode={explorerMode}
        onModeChange={setExplorerModeOverride}
        onOpenResult={openResult}
        onSelectRun={(runID) => navigate(`/runs/${runID}`)}
      />

      {explorerMode === "trend" && (
        <WhatChanged
          prompts={promptSummaries}
          onOpenResult={(id, context) => openResult([id], context)}
        />
      )}

      <EvidenceOverview
        overview={data}
        prompts={promptSummaries}
        onOpenResult={openResult}
        onOpenDomain={(domain) => setSelectedCitationDomain(domain.domain)}
      />

      <CitationSourcesDrilldown
        businessId={business.id}
        domain={selectedCitationDomain}
        open={selectedCitationDomain !== undefined}
        onOpenChange={(open) => {
          if (!open) setSelectedCitationDomain(undefined)
        }}
        onOpenResult={openResult}
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

function OverviewFrame({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-4">
      <OverviewHeader />
      {children}
    </div>
  )
}

function OverviewHeader() {
  return (
    <PageHeader
      title="Your visibility brief"
      description="Your latest AI visibility signals and what changed."
      actions={
        <Link
          className="text-sm text-muted-foreground underline underline-offset-4 hover:text-foreground"
          to="/methodology"
        >
          How we measure
        </Link>
      }
    />
  )
}

function VisibilityCard({
  overview,
  mode,
  onModeChange,
  onOpenResult,
  onSelectRun,
}: {
  overview: Overview
  mode: ExplorerMode
  onModeChange: (mode: ExplorerMode) => void
  onOpenResult: (ids: string[], context?: string) => void
  onSelectRun: (runID: string) => void
}) {
  const trend = overview.visibility?.trend ?? []
  const latestPoint = trend[trend.length - 1]
  const [snapshotRunID, setSnapshotRunID] = useState<string>()
  const [range, setRange] = useState<TrendRange>("last-12")
  const [selectedTrendRunID, setSelectedTrendRunID] = useState<string>()
  const effectiveRange: TrendRange =
    trend.length <= 12 && range === "last-12" ? "all" : range
  const snapshotPoint =
    trend.find((point) => point.runId === snapshotRunID) ?? latestPoint
  const snapshotPointIndex = trend.findIndex(
    (point) => point.runId === snapshotPoint.runId
  )
  const previousSnapshotPoint =
    snapshotPointIndex > 0 ? trend[snapshotPointIndex - 1] : undefined
  const visibleTrend =
    effectiveRange === "all"
      ? trend
      : trend.slice(effectiveRange === "last-4" ? -4 : -12)
  const selectedTrendPoint = visibleTrend.find(
    (point) => point.runId === selectedTrendRunID
  )
  const headlinePoint = mode === "snapshot" ? snapshotPoint : latestPoint

  const changeMode = (values: string[]) => {
    const next = values[0]
    if (next === "snapshot" || next === "trend") onModeChange(next)
  }
  const changeRange = (values: string[]) => {
    const next = values[0]
    if (next === "last-4" || next === "last-12" || next === "all") {
      setRange(next)
      setSelectedTrendRunID(undefined)
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex flex-col gap-1">
          <CardTitle>AI visibility</CardTitle>
          <div className="font-heading text-5xl font-semibold tracking-tight tabular-nums sm:text-6xl">
            {formatPercent(headlinePoint.percent)}
          </div>
          <CardDescription>
            {mode === "snapshot" ? "Analyzed run" : "Latest"} ·{" "}
            {longDate(headlinePoint.scheduledFor)}
          </CardDescription>
        </div>
        <ToggleGroup
          value={[mode]}
          onValueChange={changeMode}
          size="sm"
          aria-label="Visibility explorer mode"
        >
          <ToggleGroupItem value="snapshot" className="min-h-11">
            Run snapshot
          </ToggleGroupItem>
          <ToggleGroupItem
            value="trend"
            className="min-h-11"
            disabled={trend.length < 2}
            title={
              trend.length < 2
                ? "Over time becomes available after a second analyzed run"
                : undefined
            }
          >
            Over time
          </ToggleGroupItem>
        </ToggleGroup>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {mode === "trend" && trend.length > 4 && (
          <div className="flex justify-end overflow-x-auto pb-1">
            <ToggleGroup
              value={[effectiveRange]}
              onValueChange={changeRange}
              size="sm"
              aria-label="Visibility trend range"
            >
              <ToggleGroupItem value="last-4" className="min-h-11">
                Last 4
              </ToggleGroupItem>
              {trend.length > 12 && (
                <ToggleGroupItem value="last-12" className="min-h-11">
                  Last 12
                </ToggleGroupItem>
              )}
              <ToggleGroupItem value="all" className="min-h-11">
                All
              </ToggleGroupItem>
            </ToggleGroup>
          </div>
        )}

        {mode === "snapshot" ? (
          <SnapshotView
            point={snapshotPoint}
            trend={trend}
            competitors={overview.topCompetitors}
            onSelectRun={setSnapshotRunID}
            onOpenResult={onOpenResult}
          />
        ) : (
          <TrendChart
            trend={visibleTrend}
            competitors={overview.topCompetitors}
            promptChanges={overview.promptChanges}
            selectedRunID={selectedTrendRunID}
            onSelectPoint={setSelectedTrendRunID}
            selectedPoint={selectedTrendPoint}
            onOpenResult={onOpenResult}
            onOpenRun={onSelectRun}
          />
        )}
      </CardContent>
      {mode === "snapshot" && (
        <CardFooter className="flex-col items-stretch gap-3 border-t sm:flex-row sm:items-center">
          {previousSnapshotPoint && (
            <DeltaBadge
              delta={snapshotPoint.percent - previousSnapshotPoint.percent}
              previousDate={previousSnapshotPoint.scheduledFor}
            />
          )}
          <div className="flex flex-wrap gap-2 sm:ml-auto">
            <Button
              type="button"
              className="min-h-11"
              disabled={snapshotPoint.resultIds.length === 0}
              onClick={() =>
                onOpenResult(
                  snapshotPoint.resultIds,
                  `Visibility responses from ${longDate(snapshotPoint.scheduledFor)}`
                )
              }
            >
              View responses
            </Button>
            <Button
              type="button"
              variant="outline"
              className="min-h-11"
              onClick={() => onSelectRun(snapshotPoint.runId)}
            >
              Open run
            </Button>
          </div>
        </CardFooter>
      )}
    </Card>
  )
}

type ExplorerMode = "snapshot" | "trend"
type TrendRange = "last-4" | "last-12" | "all"

interface SnapshotDatum {
  key: string
  name: string
  percent: number
  mentioned: number
  analyzed: number
  color: string
  resultIds: string[]
}

function SnapshotView({
  point,
  trend,
  competitors,
  onSelectRun,
  onOpenResult,
}: {
  point: VisibilityPoint
  trend: VisibilityPoint[]
  competitors: CompetitorSummary[]
  onSelectRun: (runID: string) => void
  onOpenResult: (ids: string[], context?: string) => void
}) {
  const competitorData = competitors
    .map((competitor) => {
      const competitorPoint = competitor.trend.find(
        (candidate) => candidate.runId === point.runId
      )
      return competitorPoint
        ? { competitor, point: competitorPoint }
        : undefined
    })
    .filter((candidate) => candidate !== undefined)
    .slice(0, MAX_SNAPSHOT_COMPETITORS)
    .map(({ competitor, point: competitorPoint }, index): SnapshotDatum => {
      return {
        key: competitor.id,
        name: competitor.name,
        percent: competitorPoint.percent,
        mentioned: competitorPoint.mentioned,
        analyzed: competitorPoint.analyzed,
        color: COMPETITOR_COLORS[index],
        resultIds: competitorPoint.resultIds,
      }
    })
  const comparisonData: SnapshotDatum[] = [
    {
      key: "you",
      name: "You",
      percent: point.percent,
      mentioned: point.mentioned,
      analyzed: point.analyzed,
      color: "var(--chart-1)",
      resultIds: point.resultIds,
    },
    ...competitorData,
  ]
  const runItems = [...trend].reverse().map((candidate) => ({
    value: candidate.runId,
    label: longDate(candidate.scheduledFor),
  }))

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <span className="text-xs font-medium text-muted-foreground">
          Choose run
        </span>
        <Select
          items={runItems}
          value={point.runId}
          onValueChange={(value) => {
            if (value) onSelectRun(value)
          }}
        >
          <SelectTrigger
            className="min-h-11 w-full sm:w-72"
            aria-label="Analyzed run"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              {runItems.map((item) => (
                <SelectItem key={item.value} value={item.value}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </div>

      {competitorData.length > 0 ? (
        <SnapshotComparison
          data={comparisonData}
          date={point.scheduledFor}
          onOpenResult={onOpenResult}
        />
      ) : (
        <MentionComposition point={point} />
      )}
    </div>
  )
}

function SnapshotComparison({
  data,
  date,
  onOpenResult,
}: {
  data: SnapshotDatum[]
  date: string
  onOpenResult: (ids: string[], context?: string) => void
}) {
  return (
    <div className="flex flex-col gap-2">
      <ChartContainer config={chartConfig} className="h-64 w-full sm:h-72">
        <BarChart
          accessibilityLayer
          data={data}
          layout="vertical"
          margin={{ left: 8, right: 44 }}
        >
          <CartesianGrid horizontal={false} />
          <XAxis type="number" domain={[0, 100]} hide />
          <YAxis
            dataKey="name"
            type="category"
            width={96}
            tickLine={false}
            axisLine={false}
            tickFormatter={(name: string) =>
              name.length > 14 ? `${name.slice(0, 13)}…` : name
            }
          />
          <ChartTooltip content={<SnapshotTooltip />} />
          <Bar dataKey="percent" radius={[0, 6, 6, 0]} barSize={22}>
            {data.map((datum) => (
              <Cell key={datum.key} fill={datum.color} />
            ))}
            <LabelList
              dataKey="percent"
              position="right"
              className="fill-foreground"
              formatter={(value) =>
                typeof value === "number" ? formatPercent(value) : ""
              }
            />
          </Bar>
        </BarChart>
      </ChartContainer>
      <nav
        className="flex gap-2 overflow-x-auto pb-1"
        aria-label="View snapshot comparison responses"
      >
        {data.map((datum) => (
          <Button
            key={datum.key}
            type="button"
            size="xs"
            variant="ghost"
            className="min-h-11 shrink-0"
            disabled={datum.resultIds.length === 0}
            onClick={() =>
              onOpenResult(
                datum.resultIds,
                `${datum.name} visibility responses from ${longDate(date)}`
              )
            }
          >
            {datum.name}: {formatPercent(datum.percent)}
          </Button>
        ))}
      </nav>
    </div>
  )
}

function SnapshotTooltip({
  active,
  payload,
}: {
  active?: boolean
  payload?: { payload: SnapshotDatum }[]
}) {
  if (!active || !payload?.length) return null
  const datum = payload[0].payload
  return (
    <div className="rounded-lg border bg-background px-3 py-2 text-xs shadow-md">
      <div className="font-medium">{datum.name}</div>
      <div className="text-muted-foreground">
        {formatPercent(datum.percent)} · {datum.mentioned} of {datum.analyzed}
      </div>
    </div>
  )
}

function MentionComposition({ point }: { point: VisibilityPoint }) {
  const absent = Math.max(0, point.analyzed - point.mentioned)
  return (
    <div className="flex flex-col gap-3">
      <h3 className="text-sm font-medium">Response composition</h3>
      <div
        role="img"
        aria-label={`${point.mentioned} responses mentioned you and ${absent} did not`}
        className="flex h-8 overflow-hidden rounded-full bg-muted"
      >
        <div className="bg-primary" style={{ width: `${point.percent}%` }} />
      </div>
      <div className="flex flex-wrap gap-x-5 gap-y-2 text-xs text-muted-foreground">
        <span>{point.mentioned} mentioned you</span>
        <span>{absent} did not mention you</span>
      </div>
    </div>
  )
}

// A plotted series: "You" plus the capped competitor lines. `key` is the row
// field recharts reads; `color` carries identity, `width` keeps "You" dominant.
interface Series {
  key: string
  name: string
  color: string
  width: number
}

function TrendChart({
  trend,
  competitors,
  promptChanges,
  selectedRunID,
  onSelectPoint,
  selectedPoint,
  onOpenResult,
  onOpenRun,
}: {
  trend: VisibilityPoint[]
  competitors: CompetitorSummary[]
  promptChanges: PromptChange[]
  selectedRunID?: string
  onSelectPoint: (runID: string) => void
  selectedPoint?: VisibilityPoint
  onOpenResult: (ids: string[], context?: string) => void
  onOpenRun: (runID: string) => void
}) {
  const competitorSeries: Series[] = competitors
    .filter(
      (competitor) =>
        competitor.trend.filter((point) =>
          trend.some((visible) => visible.runId === point.runId)
        ).length >= 2
    )
    .slice(0, MAX_TREND_COMPETITOR_LINES)
    .map((c, i) => ({
      key: c.id,
      name: c.name,
      color: COMPETITOR_COLORS[i],
      width: 2,
    }))
  const series: Series[] = [
    { key: "you", name: "You", color: "var(--chart-1)", width: 2.5 },
    ...competitorSeries,
  ]
  // Competitor trends are aligned point-for-point with the visibility trend
  // (same analyzed runs), so index each competitor's percent by run_id and merge
  // it onto the matching row.
  const competitorPercentByRun = new Map<string, Map<string, number>>(
    competitorSeries.map((s) => {
      const comp = competitors.find((c) => c.id === s.key)!
      return [s.key, new Map(comp.trend.map((p) => [p.runId, p.percent]))]
    })
  )
  const data = trend.map((point) => {
    const row: Record<string, number | VisibilityPoint> = {
      x: dateMs(point.scheduledFor),
      point,
      you: point.percent,
    }
    for (const s of competitorSeries) {
      const value = competitorPercentByRun.get(s.key)?.get(point.runId)
      if (value !== undefined) row[s.key] = value
    }
    return row
  })
  const minX = data[0].x as number
  const maxX = data[data.length - 1].x as number
  const tickStep = Math.max(1, Math.ceil((data.length - 1) / 4))
  const xTicks = data
    .filter(
      (_, index) =>
        index === 0 || index === data.length - 1 || index % tickStep === 0
    )
    .map((datum) => datum.x as number)
  // A change is meaningful only between observed points in this window: exclude
  // anything on/before its first run and anything after its last.
  const markers = promptChanges
    .map((change) => ({
      ms: dateMs(change.date),
      date: change.date,
      text: describeChange(change),
    }))
    .filter((marker) => marker.ms > minX && marker.ms <= maxX)

  return (
    <div className="flex flex-col gap-2">
      <div>
        <ChartContainer config={chartConfig} className="h-64 w-full sm:h-72">
          <LineChart
            accessibilityLayer
            data={data}
            margin={{ left: 4, right: 12, top: 8 }}
            onClick={(state) => {
              const point = (
                state as unknown as {
                  activePayload?: { payload: { point: VisibilityPoint } }[]
                }
              ).activePayload?.[0]?.payload.point
              if (point) onSelectPoint(point.runId)
            }}
          >
            <CartesianGrid vertical={false} />
            <XAxis
              dataKey="x"
              type="number"
              scale="time"
              domain={[minX, maxX]}
              ticks={xTicks}
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
              tickFormatter={(v: number) => `${v}%`}
            />
            <ChartTooltip cursor content={<TrendTooltip series={series} />} />
            {markers.map((m) => (
              <ReferenceLine
                key={m.ms}
                x={m.ms}
                stroke="var(--muted-foreground)"
                strokeDasharray="4 4"
              />
            ))}
            {/* Competitors first, then "You" — later marks render on top, so the
                primary line always sits above the secondary ones. */}
            {competitorSeries.map((s) => (
              <Line
                key={s.key}
                dataKey={s.key}
                name={s.name}
                type="linear"
                stroke={s.color}
                strokeWidth={s.width}
                dot={false}
                activeDot={{ r: 4 }}
              />
            ))}
            <Line
              dataKey="you"
              name="You"
              type="linear"
              stroke="var(--chart-1)"
              strokeWidth={2.5}
              dot={{ r: 3 }}
              activeDot={{ r: 5 }}
            />
          </LineChart>
        </ChartContainer>
      </div>
      {markers.length > 0 && (
        <details>
          <summary className="flex min-h-11 cursor-pointer items-center text-xs font-medium text-muted-foreground">
            Prompt changes in this range ({markers.length})
          </summary>
          <ul className="flex flex-col gap-1 pb-2 text-xs text-muted-foreground">
            {markers.map((marker) => (
              <li key={marker.ms}>
                <span className="font-medium text-foreground">
                  {longDate(marker.date)}:
                </span>{" "}
                {marker.text}
              </li>
            ))}
          </ul>
        </details>
      )}
      {trend.length === 2 && (
        <p className="text-sm font-medium">
          Before: {shortDate(dateMs(trend[0].scheduledFor))}{" "}
          {formatPercent(trend[0].percent)} → After:{" "}
          {shortDate(dateMs(trend[1].scheduledFor))}{" "}
          {formatPercent(trend[1].percent)}
        </p>
      )}
      {competitorSeries.length > 0 && <ChartLegend series={series} />}
      <nav
        className="flex gap-2 overflow-x-auto pb-1"
        aria-label="Select visibility point by monitoring run"
      >
        {trend.map((point) => (
          <Button
            key={point.runId}
            type="button"
            size="xs"
            variant={selectedRunID === point.runId ? "secondary" : "ghost"}
            className="min-h-11 shrink-0"
            aria-pressed={selectedRunID === point.runId}
            onClick={() => onSelectPoint(point.runId)}
          >
            {shortDate(dateMs(point.scheduledFor))} ·{" "}
            {formatPercent(point.percent)}
          </Button>
        ))}
      </nav>
      {selectedPoint && (
        <div className="flex flex-col gap-3 rounded-lg border bg-muted/20 p-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <div className="font-medium">
              {longDate(selectedPoint.scheduledFor)} ·{" "}
              {formatPercent(selectedPoint.percent)}
            </div>
            <div className="text-sm text-muted-foreground">
              Mentioned in {selectedPoint.mentioned} of {selectedPoint.analyzed}{" "}
              analyzed responses
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              variant="outline"
              className="min-h-11"
              disabled={selectedPoint.resultIds.length === 0}
              onClick={() =>
                onOpenResult(
                  selectedPoint.resultIds,
                  `Visibility responses from ${longDate(selectedPoint.scheduledFor)}`
                )
              }
            >
              View responses
            </Button>
            <Button
              type="button"
              variant="outline"
              className="min-h-11"
              onClick={() => onOpenRun(selectedPoint.runId)}
            >
              Open run
            </Button>
          </div>
        </div>
      )}
      <p className="text-xs text-muted-foreground">
        Select a point or dated run to inspect it before opening its evidence.
        {markers.length > 0 &&
          " Dashed lines mark weeks where the prompt set changed."}
      </p>
    </div>
  )
}

// A color-alone chart isn't accessible (some slots sit under 3:1 contrast on a
// light card — dataviz skill relief rule), so the legend names each line too.
function ChartLegend({ series }: { series: Series[] }) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
      {series.map((s) => (
        <span
          key={s.key}
          className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground"
        >
          <svg width="18" height="6" aria-hidden className="shrink-0">
            <line
              x1="0"
              y1="3"
              x2="18"
              y2="3"
              stroke={s.color}
              strokeWidth={s.width}
            />
          </svg>
          <span className="truncate">{s.name}</span>
        </span>
      ))}
    </div>
  )
}

// describeChange names a day's prompt-set change for the marker tooltip: the
// first segment carries the noun (pluralized), later segments just count + verb,
// e.g. "1 prompt replaced" or "2 prompts added, 1 replaced".
function describeChange(change: PromptChange): string {
  const segments: { n: number; verb: string }[] = []
  if (change.added > 0) segments.push({ n: change.added, verb: "added" })
  if (change.replaced > 0)
    segments.push({ n: change.replaced, verb: "replaced" })
  if (change.retired > 0) segments.push({ n: change.retired, verb: "retired" })
  if (segments.length === 0) return "Prompt set changed"
  return segments
    .map((s, i) =>
      i === 0
        ? `${s.n} prompt${s.n === 1 ? "" : "s"} ${s.verb}`
        : `${s.n} ${s.verb}`
    )
    .join(", ")
}

function TrendTooltip({
  active,
  payload,
  series,
}: {
  active?: boolean
  payload?: {
    dataKey?: string
    value?: number
    payload: { point: VisibilityPoint }
  }[]
  series: Series[]
}) {
  if (!active || !payload?.length) return null
  const point = payload[0].payload.point
  const valueByKey = new Map(payload.map((p) => [p.dataKey, p.value]))
  return (
    <div className="min-w-40 rounded-lg border bg-background px-3 py-2 text-xs shadow-md">
      <div className="font-medium">{shortDate(dateMs(point.scheduledFor))}</div>
      <div className="mt-1 flex flex-col gap-0.5">
        {series.map((s) => {
          const value = valueByKey.get(s.key)
          if (value === undefined) return null
          return (
            <div
              key={s.key}
              className="flex items-center justify-between gap-3"
            >
              <span className="flex min-w-0 items-center gap-1.5">
                <svg width="14" height="6" aria-hidden className="shrink-0">
                  <line
                    x1="0"
                    y1="3"
                    x2="14"
                    y2="3"
                    stroke={s.color}
                    strokeWidth={s.width}
                  />
                </svg>
                <span className="truncate">{s.name}</span>
              </span>
              <span className="text-muted-foreground tabular-nums">
                {formatPercent(value)}
              </span>
            </div>
          )
        })}
      </div>
      <div className="mt-1 text-muted-foreground">
        You: mentioned in {point.mentioned} of {point.analyzed}
      </div>
    </div>
  )
}

function DeltaBadge({
  delta,
  previousDate,
}: {
  delta: number
  previousDate: string
}) {
  const rounded = Math.round(delta * 10) / 10
  const sign = rounded > 0 ? "+" : ""
  return (
    <Badge variant={rounded < 0 ? "destructive" : "secondary"}>
      {sign}
      {rounded.toFixed(1)} pts vs {shortDate(dateMs(previousDate))}
    </Badge>
  )
}

function PartialRunBanner({ overview }: { overview: Overview }) {
  const run = overview.latestRun
  // The overview payload carries run status but not per-prompt success counts, so
  // the banner names the condition and links to the failed responses rather than
  // inventing an "N of M" figure (design 06 partial-run state).
  if (
    !run ||
    (run.status !== RunStatus.PARTIAL && run.status !== RunStatus.FAILED)
  ) {
    return null
  }
  return (
    <Alert variant="destructive">
      <TriangleAlert />
      <AlertTitle>
        {run.status === RunStatus.FAILED
          ? "The latest run failed"
          : "Some prompts failed in the latest run"}
      </AlertTitle>
      <AlertDescription>
        <Link
          to={`/runs/${run.id}?status=failed`}
          className="underline underline-offset-2"
        >
          Review the failed responses
        </Link>
      </AlertDescription>
    </Alert>
  )
}

type PromptChangeState = "gained" | "lost"

interface LatestPromptChange {
  prompt: PromptSummary
  state: PromptChangeState
  resultId: string
}

function WhatChanged({
  prompts,
  onOpenResult,
}: {
  prompts: PromptSummary[]
  onOpenResult: (id: string, context: string) => void
}) {
  const changes = latestPromptChanges(prompts)
  const withoutBaseline = prompts.filter(
    (prompt) => prompt.trend.length < 2
  ).length
  const lost = changes.filter((change) => change.state === "lost")
  const gained = changes.filter((change) => change.state === "gained")

  return (
    <section aria-labelledby="what-changed-title">
      <Card>
        <CardHeader>
          <CardTitle>
            <h2 id="what-changed-title">What changed</h2>
          </CardTitle>
          <CardDescription>
            Each question compares its two latest analyzed responses.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {changes.length === 0 ? (
            <PanelEmpty>
              No questions gained or lost visibility in the latest comparison.
            </PanelEmpty>
          ) : (
            <div className="grid gap-6 md:grid-cols-2">
              <ChangeGroup
                title="No longer visible"
                items={lost}
                badgeVariant="destructive"
                icon={<ArrowDownRight data-icon="inline-start" />}
                empty="No questions lost visibility."
                onOpenResult={onOpenResult}
              />
              <ChangeGroup
                title="Now visible"
                items={gained}
                badgeVariant="secondary"
                icon={<ArrowUpRight data-icon="inline-start" />}
                empty="No questions gained visibility."
                onOpenResult={onOpenResult}
              />
            </div>
          )}
        </CardContent>
        {withoutBaseline > 0 && (
          <CardFooter className="border-t text-sm text-muted-foreground">
            {withoutBaseline} {pluralize(withoutBaseline, "question")}{" "}
            {withoutBaseline === 1 ? "needs" : "need"} another analyzed response
            before a change can be measured.
          </CardFooter>
        )}
      </Card>
    </section>
  )
}

function ChangeGroup({
  title,
  items,
  badgeVariant,
  icon,
  empty,
  onOpenResult,
}: {
  title: string
  items: LatestPromptChange[]
  badgeVariant: "destructive" | "secondary"
  icon: React.ReactNode
  empty: string
  onOpenResult: (id: string, context: string) => void
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-sm font-medium">{title}</h3>
        <Badge variant={badgeVariant} className="tabular-nums">
          {icon}
          {items.length}
        </Badge>
      </div>
      {items.length === 0 ? (
        <PanelEmpty>{empty}</PanelEmpty>
      ) : (
        <div className="flex flex-col gap-0.5">
          {items.map((item) => (
            <QuestionRow
              key={item.prompt.id}
              prompt={item.prompt}
              onClick={() =>
                onOpenResult(
                  item.resultId,
                  `Latest response for “${item.prompt.text}”`
                )
              }
            />
          ))}
        </div>
      )}
    </div>
  )
}

function EvidenceOverview({
  overview,
  prompts,
  onOpenResult,
  onOpenDomain,
}: {
  overview: Overview
  prompts: PromptSummary[]
  onOpenResult: (ids: string[], context?: string) => void
  onOpenDomain: (domain: DomainStat) => void
}) {
  const absent = prompts.filter(
    (prompt) => prompt.latestResultId !== undefined && !prompt.mentioned
  )
  return (
    <section
      className="flex flex-col gap-3"
      aria-labelledby="evidence-overview-title"
    >
      <div>
        <h2 id="evidence-overview-title" className="text-xl font-semibold">
          Explore the evidence
        </h2>
        <p className="text-sm text-muted-foreground">
          Open any item to see the responses behind it.
        </p>
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        <Panel
          title="Questions to review"
          description={`${absent.length} latest ${pluralize(absent.length, "response")} ${absent.length === 1 ? "does" : "do"} not mention you`}
          footer={
            <Link
              to="/prompts"
              className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
            >
              View all questions
              <ArrowRight className="size-4" />
            </Link>
          }
        >
          {absent.length === 0 ? (
            <PanelEmpty>No measured questions are currently absent.</PanelEmpty>
          ) : (
            absent
              .slice(0, BRIEF_LIST_LIMIT)
              .map((prompt) => (
                <QuestionRow
                  key={prompt.id}
                  prompt={prompt}
                  onClick={() =>
                    onOpenResult(
                      [prompt.latestResultId!],
                      `Latest response where you are absent: “${prompt.text}”`
                    )
                  }
                />
              ))
          )}
        </Panel>
        <CompetitorsPanel overview={overview} onOpenResult={onOpenResult} />
        <DomainsPanel overview={overview} onOpenDomain={onOpenDomain} />
        <ThemesPanel overview={overview} onOpenResult={onOpenResult} />
      </div>
    </section>
  )
}

function QuestionRow({
  prompt,
  onClick,
}: {
  prompt: PromptSummary
  onClick: () => void
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      onClick={onClick}
      className="h-auto min-h-11 w-full justify-between gap-3 px-2 py-2 text-left whitespace-normal"
    >
      <span className="line-clamp-2">{prompt.text}</span>
      <ArrowRight data-icon="inline-end" />
    </Button>
  )
}

function latestPromptChanges(prompts: PromptSummary[]): LatestPromptChange[] {
  return prompts.flatMap((prompt) => {
    if (prompt.trend.length < 2) return []
    const current = prompt.trend[prompt.trend.length - 1]
    const previous = prompt.trend[prompt.trend.length - 2]
    let state: PromptChangeState | undefined
    if (!previous.mentioned && current.mentioned) state = "gained"
    if (previous.mentioned && !current.mentioned) state = "lost"
    if (!state) return []
    return [
      {
        prompt,
        state,
        resultId: current.resultId,
      },
    ]
  })
}

function ThemesPanel({
  overview,
  onOpenResult,
}: {
  overview: Overview
  onOpenResult: (ids: string[], context?: string) => void
}) {
  return (
    <Panel
      title="Common themes"
      description="Keywords recurring across analyzed responses"
    >
      {overview.topKeywords.length === 0 ? (
        <PanelEmpty>No keywords yet.</PanelEmpty>
      ) : (
        overview.topKeywords
          .slice(0, BRIEF_LIST_LIMIT)
          .map((keyword) => (
            <StatRow
              key={keyword.keyword}
              label={keyword.keyword}
              value={`${keyword.resultIds.length} ${pluralize(keyword.resultIds.length, "response")}`}
              disabled={keyword.resultIds.length === 0}
              onClick={() =>
                onOpenResult(
                  keyword.resultIds,
                  `Responses associated with “${keyword.keyword}”`
                )
              }
            />
          ))
      )}
    </Panel>
  )
}

function DomainsPanel({
  overview,
  onOpenDomain,
}: {
  overview: Overview
  onOpenDomain: (domain: DomainStat) => void
}) {
  return (
    <Panel
      title="Cited sources"
      description="Domains ranked by the responses citing them"
    >
      {overview.topCitedDomains.length === 0 ? (
        <PanelEmpty>No citations yet.</PanelEmpty>
      ) : (
        overview.topCitedDomains
          .slice(0, BRIEF_LIST_LIMIT)
          .map((domain) => (
            <StatRow
              key={domain.domain}
              label={domain.domain}
              value={`${domain.resultIds.length} ${pluralize(domain.resultIds.length, "response")}`}
              disabled={domain.resultIds.length === 0}
              onClick={() => onOpenDomain(domain)}
            />
          ))
      )}
    </Panel>
  )
}

function CompetitorsPanel({
  overview,
  onOpenResult,
}: {
  overview: Overview
  onOpenResult: (ids: string[], context?: string) => void
}) {
  // top_competitors is truncated to the top-3 discovered, so the backlog count
  // comes from the server's untruncated discovered_total.
  const discovered = overview.discoveredTotal
  return (
    <Panel
      title="Competitors appearing"
      description="Ranked by coverage across analyzed responses"
      footer={
        <Link
          to={
            discovered > 0 ? "/competitors?status=discovered" : "/competitors"
          }
          className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
        >
          {discovered > 0
            ? `${discovered} discovered to review`
            : "View all competitors"}
          <ArrowRight className="size-4" />
        </Link>
      }
    >
      {overview.topCompetitors.length === 0 ? (
        <PanelEmpty>No competitors yet.</PanelEmpty>
      ) : (
        overview.topCompetitors
          .slice(0, BRIEF_LIST_LIMIT)
          .map((competitor) => (
            <CompetitorRow
              key={competitor.id}
              competitor={competitor}
              onClick={() =>
                onOpenResult(
                  competitor.resultIds,
                  `Responses mentioning ${competitor.name}`
                )
              }
            />
          ))
      )}
    </Panel>
  )
}

function CompetitorRow({
  competitor,
  onClick,
}: {
  competitor: CompetitorSummary
  onClick: () => void
}) {
  const disabled = competitor.resultIds.length === 0
  return (
    <Button
      type="button"
      variant="ghost"
      disabled={disabled}
      onClick={onClick}
      className="h-auto min-h-11 w-full justify-between gap-2 px-2 py-2 text-left whitespace-normal"
    >
      <span className="flex min-w-0 items-center gap-2">
        <span className="truncate">{competitor.name}</span>
        {competitor.status === CompetitorStatus.DISCOVERED && (
          <Badge variant="outline">discovered</Badge>
        )}
      </span>
      <span className="shrink-0 text-muted-foreground tabular-nums">
        {formatPercent(competitor.mentionPercent)}
      </span>
    </Button>
  )
}

function Panel({
  title,
  description,
  footer,
  children,
}: {
  title: string
  description: string
  footer?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <Card className="h-full">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col gap-0.5">
        {children}
      </CardContent>
      {footer && <CardFooter className="border-t">{footer}</CardFooter>}
    </Card>
  )
}

function StatRow({
  label,
  value,
  disabled,
  onClick,
}: {
  label: string
  value: string
  disabled?: boolean
  onClick: () => void
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      disabled={disabled}
      onClick={onClick}
      className="h-auto min-h-11 w-full justify-between gap-2 px-2 py-2 text-left whitespace-normal"
    >
      <span className="truncate">{label}</span>
      <span className="shrink-0 text-muted-foreground tabular-nums">
        {value}
      </span>
    </Button>
  )
}

function PanelEmpty({ children }: { children: React.ReactNode }) {
  return (
    <Empty className="min-h-24 p-4">
      <EmptyHeader>
        <EmptyDescription>{children}</EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}

// Trend is empty: no analyzed results exist yet. Distinguish "no run", "run in
// progress", and "awaiting analysis" so the honest state shows (design 06).
function NoDataState({ overview }: { overview: Overview }) {
  const run = overview.latestRun
  if (!run) {
    return (
      <SectionMessage
        title="No runs yet"
        description="Your visibility appears here after your first weekly monitoring run completes."
      />
    )
  }
  if (run.status === RunStatus.RUNNING) {
    return (
      <Empty className="border">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <LayoutDashboard />
          </EmptyMedia>
          <EmptyTitle>First run in progress</EmptyTitle>
          <EmptyDescription>
            <div className="w-full max-w-xs pt-2 text-left">
              <RunStageStrip run={run} variant="full" />
            </div>
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }
  return (
    <SectionMessage
      title="Analyzing responses"
      description="Your run has finished and its responses are being analyzed. Your visibility appears here shortly."
    />
  )
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
          <LayoutDashboard />
        </EmptyMedia>
        <EmptyTitle>{title}</EmptyTitle>
        <EmptyDescription>{description}</EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}

function OverviewSkeleton() {
  return (
    <div className="flex flex-col gap-4">
      <Skeleton className="h-7 w-32" />
      <Skeleton className="h-64 w-full" />
      <div className="grid gap-4 md:grid-cols-3">
        <Skeleton className="h-48 w-full" />
        <Skeleton className="h-48 w-full" />
        <Skeleton className="h-48 w-full" />
      </div>
    </div>
  )
}

function formatPercent(value: number): string {
  return `${(Math.round(value * 10) / 10).toFixed(1)}%`
}

// scheduled_for is a plain date (YYYY-MM-DD); parse as local midnight, matching
// the Runs page, so trend ticks land on the intended day.
function dateMs(scheduledFor: string): number {
  return new Date(`${scheduledFor}T00:00:00`).getTime()
}

function longDate(scheduledFor: string): string {
  return new Date(`${scheduledFor}T00:00:00`).toLocaleDateString(undefined, {
    year: "numeric",
    month: "long",
    day: "numeric",
  })
}

function pluralize(count: number, singular: string): string {
  return count === 1 ? singular : `${singular}s`
}

function shortDate(ms: number): string {
  return new Date(ms).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  })
}
