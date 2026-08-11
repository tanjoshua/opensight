import { useState } from "react"

import { dateMs, formatPercent, longDate, shortDate } from "@/lib/format"
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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { SnapshotView } from "./snapshot-view"
import { TrendChart } from "./trend-chart"
import type { ExplorerMode, Overview } from "./shared"

type TrendRange = "last-4" | "last-12" | "all"

// The adaptive visibility explorer: one headline stat over either a single
// analyzed run (snapshot) or the run history (trend).
export function VisibilityCard({
  overview,
  mode,
  runInterval,
  onModeChange,
  onOpenResult,
  onSelectRun,
}: {
  overview: Overview
  mode: ExplorerMode
  runInterval: string
  onModeChange: (mode: ExplorerMode) => void
  onOpenResult: (ids: string[], context?: string) => void
  onSelectRun: (runID: string) => void
}) {
  const trend = overview.visibility?.trend ?? []
  const latestPoint = trend[trend.length - 1]
  const [snapshotRunID, setSnapshotRunID] = useState<string>()
  const [range, setRange] = useState<TrendRange>("last-12")
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
  const headlinePoint = mode === "snapshot" ? snapshotPoint : latestPoint

  const changeMode = (values: string[]) => {
    const next = values[0]
    if (next === "snapshot" || next === "trend") onModeChange(next)
  }
  const changeRange = (values: string[]) => {
    const next = values[0]
    if (next === "last-4" || next === "last-12" || next === "all") {
      setRange(next)
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
            runInterval={runInterval}
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
