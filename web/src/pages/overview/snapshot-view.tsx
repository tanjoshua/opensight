import type {
  CompetitorSummary,
  VisibilityPoint,
} from "@/gen/opensight/v1/overview_pb"
import { longDate } from "@/lib/format"
import { RunSnapshot } from "@/components/run-snapshot"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

// The Brief's Run snapshot mode: pick any analyzed run, then read it with the
// same snapshot the run detail page shows.
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

      <RunSnapshot
        point={point}
        competitors={competitors}
        onOpenResult={onOpenResult}
      />
    </div>
  )
}
