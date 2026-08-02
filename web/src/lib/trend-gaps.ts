// Trend charts draw one row per analyzed run against a true time x-axis. A
// lapsed subscription stops runs entirely (BILL-10 — no spend while lapsed),
// so without this the missing weeks/months would be drawn as one straight
// line segment: visual interpolation across a period nothing was observed.
// withRunGaps breaks the line instead, by inserting a valueless row at the
// midpoint of any interval wider than twice the plan's run interval —
// Recharts' connectNulls defaults to false, so a row carrying no series key
// breaks every line through it while the axis (computed from the real rows,
// before this runs) keeps its true scale.
//
// rows must already be sorted ascending by x (every call site builds it that
// way off a run's scheduled_for). An unrecognized run interval disables gap
// insertion rather than guessing a threshold (design 08's rule: an unknown
// interval is never defaulted).
export function withRunGaps<T extends { x: number }>(
  rows: T[],
  runInterval: string
): (T | { x: number })[] {
  const intervalDays = runIntervalDays(runInterval)
  if (intervalDays === undefined || rows.length < 2) return rows
  const maxGapMs = 2 * intervalDays * 24 * 60 * 60 * 1000
  const out: (T | { x: number })[] = [rows[0]]
  for (let i = 1; i < rows.length; i++) {
    const prev = rows[i - 1]
    const curr = rows[i]
    // The inserted row is deliberately not a T — it carries none of T's series
    // keys — so a chart line breaks through it instead of interpolating.
    if (curr.x - prev.x > maxGapMs) {
      out.push({ x: Math.round((prev.x + curr.x) / 2) })
    }
    out.push(curr)
  }
  return out
}

// runIntervalDays maps a plan's run_interval (design 08's catalog string) to
// its length in days.
function runIntervalDays(interval: string): number | undefined {
  switch (interval) {
    case "daily":
      return 1
    case "weekly":
      return 7
    case "monthly":
      return 30
    default:
      return undefined
  }
}
