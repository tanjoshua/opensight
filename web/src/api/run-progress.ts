// Pure derivation of a run's user-facing progress (RUNS-3, design 04): a
// four-stage strip — Preparing, Asking ChatGPT, Analyzing, Done — computed
// from the counts ListRuns/GetOverview already carry on Run, never from
// Temporal workflow history (which stays an ops-only surface). This is the
// one place a stage gets derived; Overview, the Runs list rows and the run
// detail page all call it so they can't disagree.
import { RunStatus } from "@/gen/opensight/v1/common_pb"
import type { Run } from "@/gen/opensight/v1/result_pb"

export type RunStageId = "preparing" | "asking" | "analyzing" | "done"
export type RunStageState = "pending" | "active" | "done" | "warning"

export interface RunStage {
  id: RunStageId
  label: string
  detail: string
  state: RunStageState
}

export interface RunProgress {
  stage: RunStageId
  label: string
  counts: { expected: number; succeeded: number; failed: number; analyzed: number }
  stages: RunStage[]
}

const STAGE_LABELS: Record<RunStageId, string> = {
  preparing: "Preparing",
  asking: "Asking ChatGPT",
  analyzing: "Analyzing",
  done: "Done",
}

export function runProgress(run: Run): RunProgress {
  const succeeded = run.succeededResults
  const failed = run.failedResults
  const analyzed = run.analyzedResults
  // Legacy-null fallback: a run persisted before RUNS-1 has no stored
  // expected_results, so the best available "N" is however many results
  // actually landed.
  const expected = run.expectedResults ?? succeeded + failed
  const completed = succeeded + failed
  const counts = { expected, succeeded, failed, analyzed }

  const stage = currentStage(run, completed, expected)
  return {
    stage,
    label: topLabel(run, stage, counts),
    counts,
    stages: buildStages(run, stage, counts),
  }
}

function currentStage(
  run: Run,
  completed: number,
  expected: number
): RunStageId {
  if (run.status !== RunStatus.RUNNING) return "done"
  if (completed === 0) return "preparing"
  if (completed < expected) return "asking"
  return "analyzing"
}

function askingDetail(counts: RunProgress["counts"]): string {
  const base = `${counts.succeeded + counts.failed} of ${counts.expected}`
  return counts.failed > 0 ? `${base} (${counts.failed} failed)` : base
}

function analyzingDetail(counts: RunProgress["counts"]): string {
  return `${counts.analyzed} of ${counts.succeeded}`
}

function topLabel(
  run: Run,
  stage: RunStageId,
  counts: RunProgress["counts"]
): string {
  if (run.status === RunStatus.RUNNING) {
    switch (stage) {
      case "preparing":
        return STAGE_LABELS.preparing
      case "asking":
        return askingDetail(counts)
      default:
        return analyzingDetail(counts)
    }
  }
  switch (run.status) {
    case RunStatus.PARTIAL:
      return `${counts.succeeded} of ${counts.expected} succeeded`
    case RunStatus.FAILED:
      return `${counts.failed} of ${counts.expected} failed`
    case RunStatus.COMPLETED:
      return "Completed"
    default:
      return STAGE_LABELS.done
  }
}

// stateFor decides a stage's icon state relative to the currently active
// stage: everything before it is done, the active one is active, everything
// after is still pending. Terminal-run warnings (below) override this for
// the Asking/Analyzing stages.
function stateFor(order: RunStageId[], id: RunStageId, active: RunStageId): RunStageState {
  const activeIndex = order.indexOf(active)
  const index = order.indexOf(id)
  if (index < activeIndex) return "done"
  if (index === activeIndex) return "active"
  return "pending"
}

const STAGE_ORDER: RunStageId[] = ["preparing", "asking", "analyzing", "done"]

function buildStages(
  run: Run,
  stage: RunStageId,
  counts: RunProgress["counts"]
): RunStage[] {
  if (run.status === RunStatus.RUNNING) {
    return STAGE_ORDER.map((id) => {
      const state = stateFor(STAGE_ORDER, id, stage)
      // A stage that hasn't started yet has nothing to report — showing its
      // future count (e.g. "Analyzing 0 of 1" while still Asking) would read
      // as progress that hasn't happened.
      const detail =
        state === "pending"
          ? ""
          : id === "asking"
            ? askingDetail(counts)
            : id === "analyzing"
              ? analyzingDetail(counts)
              : ""
      return { id, label: STAGE_LABELS[id], detail, state }
    })
  }

  // Terminal run: Preparing always completed. Asking carries a warning when
  // the run didn't fully succeed (partial/failed) — that's a real, known-bad
  // outcome. Analyzing is different: FinalizeRun runs before AnalyzeRun (see
  // run_workflow.go), so every healthy run sits with analysisCompletedAt
  // unset for the entire — often multi-minute, 20-prompt — window while
  // analysis is still in flight. There's no field distinguishing "still
  // analyzing" from "analysis actually failed/flagged for re-analysis"
  // (design 04/05 doesn't surface that distinction anywhere else either), so
  // treating unset as a warning would red-flag the common case, not the rare
  // one. Render it as an in-progress state (spinner, neutral text) instead —
  // matches Overview's own copy for this state ("...being analyzed...
  // shortly").
  const askingWarning = run.status === RunStatus.PARTIAL || run.status === RunStatus.FAILED
  const stillAnalyzing = run.analysisCompletedAt === undefined

  return [
    { id: "preparing", label: STAGE_LABELS.preparing, detail: "", state: "done" },
    {
      id: "asking",
      label: STAGE_LABELS.asking,
      detail:
        run.status === RunStatus.FAILED
          ? `${counts.failed} of ${counts.expected} failed`
          : `${counts.succeeded} of ${counts.expected} succeeded`,
      state: askingWarning ? "warning" : "done",
    },
    {
      id: "analyzing",
      label: STAGE_LABELS.analyzing,
      detail: stillAnalyzing ? "Still analyzing" : analyzingDetail(counts),
      state: stillAnalyzing ? "active" : "done",
    },
    // Not done until analysis lands — a checked Done above a spinning
    // Analyzing reads as the pipeline contradicting itself.
    {
      id: "done",
      label: STAGE_LABELS.done,
      detail: "",
      state: stillAnalyzing ? "pending" : "done",
    },
  ]
}
