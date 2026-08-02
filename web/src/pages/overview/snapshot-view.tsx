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
import { formatPercent, longDate } from "@/lib/format"
import { Button } from "@/components/ui/button"
import { ChartContainer, ChartTooltip } from "@/components/ui/chart"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { chartConfig, COMPETITOR_COLORS } from "./shared"

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

export function SnapshotView({
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
