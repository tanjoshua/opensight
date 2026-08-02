import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts"

import { usePlan } from "@/api/hooks"
import type { CompetitorTrendPoint } from "@/gen/opensight/v1/competitor_pb"
import { dateMs, formatPercent, shortDate } from "@/lib/format"
import { percentYAxisProps, timeXAxisProps } from "@/lib/trend-axis"
import { withRunGaps } from "@/lib/trend-gaps"
import { Button } from "@/components/ui/button"
import {
  ChartContainer,
  ChartTooltip,
  type ChartConfig,
} from "@/components/ui/chart"
import { EmptyNote } from "./section-parts"

const chartConfig = {
  percent: { label: "Coverage", color: "var(--primary)" },
} satisfies ChartConfig

// One competitor's coverage over its analyzed runs. A single analyzed run has
// no line to draw, so it renders as one labeled dot instead.
export function TrendChart({
  trend,
  onSelectRun,
}: {
  trend: CompetitorTrendPoint[]
  onSelectRun: (runID: string) => void
}) {
  const { plan } = usePlan()
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
  // Break the line across any period lapsed monitoring left uncollected
  // (BILL-10), instead of interpolating straight across it.
  const chartData = withRunGaps(data, plan?.runInterval ?? "")
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <ChartContainer config={chartConfig} className="h-56 w-full min-w-0">
        <LineChart
          accessibilityLayer
          data={chartData}
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
            {...timeXAxisProps}
            domain={["dataMin", "dataMax"]}
            ticks={data.map((datum) => datum.x)}
          />
          <YAxis {...percentYAxisProps} />
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
  payload?: { payload: { point?: CompetitorTrendPoint } }[]
}) {
  if (!active || !payload?.length) return null
  const point = payload[0].payload.point
  // A gap row (BILL-10, trend-gaps.ts) carries no point — nothing to show.
  if (!point) return null
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
