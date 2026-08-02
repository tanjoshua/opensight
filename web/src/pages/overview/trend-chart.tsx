import {
  CartesianGrid,
  Line,
  LineChart,
  ReferenceLine,
  XAxis,
  YAxis,
} from "recharts"

import type {
  CompetitorSummary,
  PromptChange,
  VisibilityPoint,
} from "@/gen/opensight/v1/overview_pb"
import { dateMs, formatPercent, longDate, shortDate } from "@/lib/format"
import { percentYAxisProps, timeXAxisProps } from "@/lib/trend-axis"
import { withRunGaps } from "@/lib/trend-gaps"
import { Button } from "@/components/ui/button"
import { ChartContainer, ChartTooltip } from "@/components/ui/chart"
import { chartConfig, COMPETITOR_COLORS } from "./shared"

// Cap the competitor lines to the top few so the chart stays legible.
const MAX_TREND_COMPETITOR_LINES = 2

// A trend chart row: a true-time x plus "point" (the source datum, read by
// the tooltip/onClick) and one numeric value per plotted series, keyed by
// Series.key below. The index signature is what lets withRunGaps
// (trend-gaps.ts) insert a valueless `{ x }` row that still satisfies this
// type — Recharts then has nothing to plot for it, breaking the line.
interface TrendRow extends Record<
  string,
  number | VisibilityPoint | undefined
> {
  x: number
  point?: VisibilityPoint
}

// A plotted series: "You" plus the capped competitor lines. `key` is the row
// field recharts reads; `color` carries identity, `width` keeps "You" dominant.
interface Series {
  key: string
  name: string
  color: string
  width: number
}

export function TrendChart({
  trend,
  competitors,
  promptChanges,
  runInterval,
  selectedRunID,
  onSelectPoint,
  selectedPoint,
  onOpenResult,
  onOpenRun,
}: {
  trend: VisibilityPoint[]
  competitors: CompetitorSummary[]
  promptChanges: PromptChange[]
  runInterval: string
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
  const data: TrendRow[] = trend.map((point) => {
    const row: TrendRow = {
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
  const minX = data[0].x
  const maxX = data[data.length - 1].x
  const tickStep = Math.max(1, Math.ceil((data.length - 1) / 4))
  const xTicks = data
    .filter(
      (_, index) =>
        index === 0 || index === data.length - 1 || index % tickStep === 0
    )
    .map((datum) => datum.x)
  // A change is meaningful only between observed points in this window: exclude
  // anything on/before its first run and anything after its last.
  const markers = promptChanges
    .map((change) => ({
      ms: dateMs(change.date),
      date: change.date,
      text: describeChange(change),
    }))
    .filter((marker) => marker.ms > minX && marker.ms <= maxX)
  // Break the line across any period lapsed monitoring left uncollected
  // (BILL-10), instead of interpolating straight across it. Computed off the
  // real rows above — minX/maxX/xTicks/markers stay true to the observed
  // data, not stretched by the inserted gap rows.
  const chartData = withRunGaps(data, runInterval)

  return (
    <div className="flex flex-col gap-2">
      <div>
        <ChartContainer config={chartConfig} className="h-64 w-full sm:h-72">
          <LineChart
            accessibilityLayer
            data={chartData}
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
            <XAxis {...timeXAxisProps} domain={[minX, maxX]} ticks={xTicks} />
            <YAxis {...percentYAxisProps} />
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
      {competitorSeries.length > 0 && <SeriesLegend series={series} />}
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
function SeriesLegend({ series }: { series: Series[] }) {
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
    payload: { point?: VisibilityPoint }
  }[]
  series: Series[]
}) {
  if (!active || !payload?.length) return null
  const point = payload[0].payload.point
  // A gap row (BILL-10, trend-gaps.ts) carries no point — nothing to show.
  if (!point) return null
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
