// RunStageStrip (RUNS-3, design 04): renders the four-stage progress derived
// by runProgress. "full" is a vertical list (run detail page, Overview's
// first-run state); "compact" is one horizontal line of pips plus the active
// stage's label (Runs list rows). No charting/DAG library — the pipeline is
// linear with one fan-out, a graph would overstate it.
import { Check, Circle, LoaderCircle, TriangleAlert } from "lucide-react"

import { runProgress, type RunStage, type RunStageState } from "@/api/run-progress"
import type { Run } from "@/gen/opensight/v1/result_pb"
import { cn } from "@/lib/utils"

export function RunStageStrip({
  run,
  variant,
  onShowFailed,
}: {
  run: Run
  variant: "full" | "compact"
  onShowFailed?: () => void
}) {
  const progress = runProgress(run)
  return variant === "full" ? (
    <div className="flex flex-col gap-3">
      {progress.stages.map((stage) => (
        <FullStageRow
          key={stage.id}
          stage={stage}
          onShowFailed={
            stage.id === "asking" && stage.state === "warning"
              ? onShowFailed
              : undefined
          }
        />
      ))}
    </div>
  ) : (
    <div className="flex items-center gap-2">
      <div className="flex items-center gap-1">
        {progress.stages.map((stage) => (
          <StagePip key={stage.id} state={stage.state} />
        ))}
      </div>
      <span className="text-sm text-muted-foreground">{progress.label}</span>
    </div>
  )
}

function FullStageRow({
  stage,
  onShowFailed,
}: {
  stage: RunStage
  onShowFailed?: () => void
}) {
  return (
    <div className="flex items-start gap-3">
      <StageIcon state={stage.state} />
      <div className="flex flex-col">
        <span className="text-sm font-medium">{stage.label}</span>
        {stage.detail !== "" && (
          <span
            className={cn(
              "text-sm",
              stage.state === "warning" ? "text-destructive" : "text-muted-foreground"
            )}
          >
            {onShowFailed ? (
              <button
                type="button"
                className="cursor-pointer underline underline-offset-2"
                onClick={onShowFailed}
              >
                {stage.detail}
              </button>
            ) : (
              stage.detail
            )}
          </span>
        )}
      </div>
    </div>
  )
}

function StageIcon({ state }: { state: RunStageState }) {
  switch (state) {
    case "done":
      return <Check className="mt-0.5 size-4 shrink-0 text-foreground" />
    case "active":
      return (
        <LoaderCircle className="mt-0.5 size-4 shrink-0 animate-spin text-foreground" />
      )
    case "warning":
      return <TriangleAlert className="mt-0.5 size-4 shrink-0 text-destructive" />
    default:
      return <Circle className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
  }
}

function StagePip({ state }: { state: RunStageState }) {
  const color =
    state === "warning"
      ? "bg-destructive"
      : state === "done"
        ? "bg-foreground"
        : state === "active"
          ? "bg-foreground animate-pulse"
          : "bg-muted-foreground/30"
  return <span className={cn("size-1.5 rounded-full", color)} aria-hidden />
}
