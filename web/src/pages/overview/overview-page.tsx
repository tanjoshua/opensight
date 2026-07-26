// Overview section (INS-1, design 06): one page answering "how visible am I and
// what changed". Headline visibility stat + weekly trend line with prompt-set
// change markers, then three compact panels (themes, cited domains, competitors).
// Every number is a door — stats open the Response drawer via their result_ids,
// and clicking a week on the trend deep-links to that run's detail page.
import { skipToken, useQuery } from "@connectrpc/connect-query"
import {
  ArrowDownRight,
  ArrowRight,
  ArrowUpRight,
  CircleMinus,
  LayoutDashboard,
  TriangleAlert,
} from "lucide-react"
import { useState } from "react"
import { Link, useNavigate } from "react-router"
import {
  CartesianGrid,
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
const MAX_COMPETITOR_LINES = 3
const COMPETITOR_COLORS = ["var(--chart-2)", "var(--chart-3)", "var(--chart-4)"]
const BRIEF_LIST_LIMIT = 5

// First run, single data point: the chart still renders as a chart (axes, grid,
// one dot) rather than swapping to a separate empty-state layout, with the
// x-axis stretched one interval past the point so the shape reads as "day one
// of a growing trend" rather than a dead end. The window doesn't assert an
// actual next-run date (cadence is plan-driven, MVP is weekly-only) — the tick
// is labeled "Next run" rather than a date for that reason.
const PROJECTED_WINDOW_MS = 7 * 24 * 60 * 60 * 1000

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

  // No analyzed history yet: either no run has happened, the first run is still
  // in flight, or results are awaiting analysis (design 06 degraded states).
  if ((data.visibility?.trend.length ?? 0) === 0) {
    return (
      <OverviewFrame overview={data}>
        <NoDataState overview={data} />
      </OverviewFrame>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <OverviewHeader overview={data} />

      <PartialRunBanner overview={data} />

      <VisibilityCard
        overview={data}
        onOpenResult={openResult}
        onSelectRun={(runID) => navigate(`/runs/${runID}`)}
      />

      <WhatChanged
        prompts={promptSummaries}
        onOpenResult={(id, context) => openResult([id], context)}
      />

      <WhereToFocus
        overview={data}
        prompts={promptSummaries}
        onOpenResult={openResult}
        onOpenDomain={(domain) => setSelectedCitationDomain(domain.domain)}
      />

      <ThemesPanel overview={data} onOpenResult={openResult} />

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

function OverviewFrame({
  children,
  overview,
}: {
  children: React.ReactNode
  overview?: Overview
}) {
  return (
    <div className="flex flex-col gap-4">
      <OverviewHeader overview={overview} />
      {children}
    </div>
  )
}

function OverviewHeader({ overview }: { overview?: Overview }) {
  const trend = overview?.visibility?.trend ?? []
  const latest = trend[trend.length - 1]
  return (
    <PageHeader
      title="Your visibility brief"
      description={
        latest
          ? `Latest analyzed run: ${longDate(latest.scheduledFor)} · ${latest.analyzed} ${pluralize(latest.analyzed, "response")} analyzed`
          : "Your latest AI visibility signals and what changed."
      }
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
  onOpenResult,
  onSelectRun,
}: {
  overview: Overview
  onOpenResult: (ids: string[], context?: string) => void
  onSelectRun: (runID: string) => void
}) {
  const current = overview.visibility?.current
  const delta = overview.visibility?.delta
  const trend = overview.visibility?.trend ?? []
  const latestPoint = trend[trend.length - 1]
  const previousPoint = trend.length > 1 ? trend[trend.length - 2] : undefined

  return (
    <Card className="overflow-hidden">
      <CardHeader className="border-b bg-muted/20">
        <CardDescription>AI visibility</CardDescription>
        <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-end">
          <div className="space-y-2">
            <CardTitle className="text-5xl font-semibold tabular-nums sm:text-6xl">
              {current === undefined ? "—" : formatPercent(current)}
            </CardTitle>
            <p className="text-sm text-muted-foreground">
              Mentioned in{" "}
              <span className="font-medium text-foreground">
                {latestPoint.mentioned} of {latestPoint.analyzed}
              </span>{" "}
              analyzed responses on {longDate(latestPoint.scheduledFor)}
            </p>
            {delta !== undefined && previousPoint && (
              <DeltaBadge
                delta={delta}
                previousDate={previousPoint.scheduledFor}
              />
            )}
          </div>
          <Button
            type="button"
            variant="outline"
            disabled={latestPoint.resultIds.length === 0}
            onClick={() =>
              onOpenResult(
                latestPoint.resultIds,
                "Responses behind your current visibility"
              )
            }
          >
            View {latestPoint.resultIds.length}{" "}
            {pluralize(latestPoint.resultIds.length, "response")}
            <ArrowRight data-icon="inline-end" />
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        <TrendChart
          trend={trend}
          competitors={overview.topCompetitors}
          promptChanges={overview.promptChanges}
          onSelectRun={onSelectRun}
        />
      </CardContent>
    </Card>
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
  onSelectRun,
}: {
  trend: VisibilityPoint[]
  competitors: CompetitorSummary[]
  promptChanges: PromptChange[]
  onSelectRun: (runID: string) => void
}) {
  const competitorSeries: Series[] = competitors
    .slice(0, MAX_COMPETITOR_LINES)
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
  // With only one run so far, stretch the axis one interval past the point so
  // the chart still reads as a chart — a dot at the left with room to grow —
  // instead of collapsing to a zero-width domain.
  const maxX =
    data.length > 1
      ? (data[data.length - 1].x as number)
      : minX + PROJECTED_WINDOW_MS
  // Markers only make sense inside the plotted window; a prompt change before the
  // first run or after the last has nothing to sit against.
  const markers = promptChanges
    .map((change) => ({
      ms: dateMs(change.date),
      text: describeChange(change),
    }))
    .filter((m) => m.ms >= minX && m.ms <= maxX)
  // Recharts ReferenceLine has no built-in hover tooltip, so a marker reports its
  // pixel position and description up here; a single HTML tip is drawn over the
  // chart at that x. Tap toggles it too (mobile has no hover).
  const [marker, setMarker] = useState<{ x: number; text: string } | null>(null)

  return (
    <div className="flex flex-col gap-2">
      <div className="relative">
        <ChartContainer config={chartConfig} className="h-56 w-full">
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
              if (point) onSelectRun(point.runId)
            }}
          >
            <CartesianGrid vertical={false} />
            <XAxis
              dataKey="x"
              type="number"
              scale="time"
              domain={[minX, maxX]}
              ticks={
                data.length > 1 ? data.map((d) => d.x as number) : [minX, maxX]
              }
              tickLine={false}
              axisLine={false}
              tickMargin={8}
              tickFormatter={(ms: number) =>
                data.length === 1 && ms === maxX ? "Next run" : shortDate(ms)
              }
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
                label={
                  <MarkerLabel
                    onEnter={(x) => setMarker({ x, text: m.text })}
                    onLeave={() => setMarker(null)}
                  />
                }
              />
            ))}
            {/* Competitors first, then "You" — later marks render on top, so the
                primary line always sits above the secondary ones. */}
            {competitorSeries.map((s) => (
              <Line
                key={s.key}
                dataKey={s.key}
                name={s.name}
                type="monotone"
                stroke={s.color}
                strokeWidth={s.width}
                dot={false}
                activeDot={{ r: 4 }}
                connectNulls
              />
            ))}
            <Line
              dataKey="you"
              name="You"
              type="monotone"
              stroke="var(--chart-1)"
              strokeWidth={2.5}
              dot={{ r: 3 }}
              activeDot={{ r: 5 }}
            />
          </LineChart>
        </ChartContainer>
        {marker && (
          <div
            className="pointer-events-none absolute top-0 z-10 max-w-40 -translate-x-1/2 rounded-lg border bg-background px-2.5 py-1.5 text-xs font-medium shadow-md"
            style={{ left: marker.x }}
          >
            {marker.text}
          </div>
        )}
      </div>
      {competitorSeries.length > 0 && <ChartLegend series={series} />}
      <nav
        className="flex gap-2 overflow-x-auto pb-1"
        aria-label="Open visibility evidence by monitoring run"
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
        {trend.length === 1
          ? "This is your starting point. The line grows as each run completes."
          : "Select a dated run to open its evidence."}
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

// MarkerLabel is a ReferenceLine label (recharts injects viewBox): a small glyph
// at the top plus a full-height transparent hit target so hovering or tapping
// anywhere on the dashed line surfaces the change description.
function MarkerLabel({
  viewBox,
  onEnter,
  onLeave,
}: {
  viewBox?: { x?: number; y?: number; height?: number }
  onEnter: (x: number) => void
  onLeave: () => void
}) {
  if (!viewBox || viewBox.x == null) return null
  const x = viewBox.x
  const y = viewBox.y ?? 0
  const height = viewBox.height ?? 0
  return (
    <g
      className="cursor-pointer"
      onMouseEnter={() => onEnter(x)}
      onMouseLeave={onLeave}
      onClick={(e) => {
        e.stopPropagation()
        onEnter(x)
      }}
    >
      <rect x={x - 6} y={y} width={12} height={height} fill="transparent" />
      <circle cx={x} cy={y} r={3} fill="var(--muted-foreground)" />
    </g>
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

type PromptChangeState = "gained" | "lost" | "still-absent"

interface LatestPromptChange {
  prompt: PromptSummary
  state: PromptChangeState
  resultId: string
  currentDate: string
  previousDate: string
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
  const groups: {
    state: PromptChangeState
    title: string
    empty: string
    icon: React.ReactNode
  }[] = [
    {
      state: "gained",
      title: "Now visible",
      empty: "No questions gained visibility.",
      icon: <ArrowUpRight className="size-4 text-emerald-700" />,
    },
    {
      state: "lost",
      title: "No longer visible",
      empty: "No questions lost visibility.",
      icon: <ArrowDownRight className="size-4 text-destructive" />,
    },
    {
      state: "still-absent",
      title: "Still absent",
      empty: "No questions stayed absent.",
      icon: <CircleMinus className="size-4 text-muted-foreground" />,
    },
  ]

  return (
    <section className="space-y-3" aria-labelledby="what-changed-title">
      <div>
        <h2 id="what-changed-title" className="text-xl font-semibold">
          What changed
        </h2>
        <p className="text-sm text-muted-foreground">
          Each question compares its two latest analyzed responses.
          {withoutBaseline > 0 &&
            ` ${withoutBaseline} ${pluralize(withoutBaseline, "question")} ${withoutBaseline === 1 ? "has" : "have"} no baseline yet.`}
        </p>
      </div>
      <div className="grid gap-4 lg:grid-cols-3">
        {groups.map((group) => {
          const items = changes.filter((change) => change.state === group.state)
          return (
            <Card key={group.state} className="gap-3">
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  {group.icon}
                  {group.title}
                  <Badge variant="outline" className="ml-auto tabular-nums">
                    {items.length}
                  </Badge>
                </CardTitle>
              </CardHeader>
              <CardContent className="flex flex-col gap-1">
                {items.length === 0 ? (
                  <PanelEmpty>{group.empty}</PanelEmpty>
                ) : (
                  items.map((item) => (
                    <button
                      key={item.prompt.id}
                      type="button"
                      className="rounded-md px-2 py-2 text-left hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
                      onClick={() =>
                        onOpenResult(
                          item.resultId,
                          `Latest response for “${item.prompt.text}”`
                        )
                      }
                    >
                      <span className="line-clamp-2 text-sm font-medium">
                        {item.prompt.text}
                      </span>
                      <span className="mt-1 block text-xs text-muted-foreground">
                        {shortDate(dateMs(item.previousDate))} →{" "}
                        {shortDate(dateMs(item.currentDate))}
                      </span>
                    </button>
                  ))
                )}
              </CardContent>
            </Card>
          )
        })}
      </div>
    </section>
  )
}

function WhereToFocus({
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
    <section className="space-y-3" aria-labelledby="where-to-focus-title">
      <div>
        <h2 id="where-to-focus-title" className="text-xl font-semibold">
          Where to focus
        </h2>
        <p className="text-sm text-muted-foreground">
          Questions reflect the latest analyzed response. Sources and
          competitors reflect evidence collected to date.
        </p>
      </div>
      <div className="grid gap-4 lg:grid-cols-3">
        <Panel
          title="Questions where you’re absent"
          description="Latest analyzed response"
          footer={
            <Link
              to="/prompts"
              className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
            >
              View all questions
              <ArrowRight className="size-3.5" />
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
        <DomainsPanel overview={overview} onOpenDomain={onOpenDomain} />
        <CompetitorsPanel overview={overview} onOpenResult={onOpenResult} />
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
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center justify-between gap-3 rounded-md px-2 py-2 text-left text-sm hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
    >
      <span className="line-clamp-2">{prompt.text}</span>
      <ArrowRight className="size-3.5 shrink-0 text-muted-foreground" />
    </button>
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
    if (!previous.mentioned && !current.mentioned) state = "still-absent"
    if (!state) return []
    return [
      {
        prompt,
        state,
        resultId: current.resultId,
        currentDate: current.scheduledFor,
        previousDate: previous.scheduledFor,
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
      description="A secondary view of keywords across your responses"
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
              count={keyword.resultIds.length}
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
      title="Top cited domains"
      description="Sources cited in evidence collected to date"
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
              count={domain.resultIds.length}
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
      title="Leading competitors"
      description="Who appears in evidence collected to date"
      footer={
        <Link
          to={
            discovered > 0 ? "/competitors?status=discovered" : "/competitors"
          }
          className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
        >
          {discovered > 0
            ? `${discovered} discovered — triage`
            : "View all competitors"}
          <ArrowRight className="size-3.5" />
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
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className="flex w-full items-center justify-between gap-2 rounded-md px-2 py-1.5 text-left text-sm enabled:cursor-pointer enabled:hover:bg-muted/50 disabled:opacity-70"
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
    </button>
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
    <Card className="gap-3">
      <CardHeader>
        <CardTitle className="text-base">{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-0.5">{children}</CardContent>
      {footer && <div className="border-t px-6 pt-3">{footer}</div>}
    </Card>
  )
}

function StatRow({
  label,
  count,
  onClick,
}: {
  label: string
  count: number
  onClick: () => void
}) {
  return (
    <button
      type="button"
      disabled={count === 0}
      onClick={onClick}
      className="flex w-full items-center justify-between gap-2 rounded-md px-2 py-1.5 text-left text-sm enabled:cursor-pointer enabled:hover:bg-muted/50 disabled:opacity-70"
    >
      <span className="truncate">{label}</span>
      <span className="shrink-0 text-muted-foreground tabular-nums">
        {count}
      </span>
    </button>
  )
}

function PanelEmpty({ children }: { children: React.ReactNode }) {
  return <p className="px-2 py-1.5 text-sm text-muted-foreground">{children}</p>
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
