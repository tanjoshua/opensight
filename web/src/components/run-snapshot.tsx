// One analyzed run's visibility, as a chart: the business against the
// competitors that appear in the same run, or a mentioned/not-mentioned
// composition when none do. Shared by the Brief's Run snapshot mode (which
// adds its own run picker) and the run detail page, so a run reads the same
// way wherever it is opened.
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  LabelList,
  XAxis,
  YAxis,
} from "recharts"

import type {
  CompetitorSummary,
  VisibilityPoint,
} from "@/gen/opensight/v1/overview_pb"
import { chartConfig, COMPETITOR_COLORS } from "@/lib/chart-series"
import { formatPercent, longDate } from "@/lib/format"
import { Button } from "@/components/ui/button"
import { ChartContainer, ChartTooltip } from "@/components/ui/chart"

// Cap the compared competitors so the bar chart stays legible.
const MAX_SNAPSHOT_COMPETITORS = 3

interface SnapshotDatum {
  key: string
  name: string
  percent: number
  mentioned: number
  analyzed: number
  color: string
  resultIds: string[]
}

export function RunSnapshot({
  point,
  competitors,
  onOpenResult,
}: {
  point: VisibilityPoint
  competitors: CompetitorSummary[]
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

  return competitorData.length > 0 ? (
    <SnapshotComparison
      data={comparisonData}
      date={point.scheduledFor}
      onOpenResult={onOpenResult}
    />
  ) : (
    <MentionComposition point={point} />
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
            // Competitor names are long ("National Dental Centre Singapore
            // (NDCS)"), and a truncated axis label leaves the reader guessing
            // which bar is whose. Give the axis room for a realistic name and
            // only elide past that.
            width={168}
            tickLine={false}
            axisLine={false}
            tickFormatter={(name: string) =>
              name.length > 26 ? `${name.slice(0, 25)}…` : name
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
