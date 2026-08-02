import type { XAxisProps, YAxisProps } from "recharts"

import { shortDate } from "@/lib/format"

// Axis configuration shared by the two trend charts (Overview's multi-series
// chart and the Competitors detail chart). They plot different series against
// the same pair of axes — a true time x-axis and a 0–100% y-axis — so only the
// axis props are shared, spread onto each chart's own <XAxis>/<YAxis>. Recharts
// discovers axes by element type, so these stay props rather than a wrapper
// component. Each chart supplies its own `domain`/`ticks`.
export const timeXAxisProps: XAxisProps = {
  dataKey: "x",
  type: "number",
  scale: "time",
  tickLine: false,
  axisLine: false,
  tickMargin: 8,
  tickFormatter: (ms: number) => shortDate(ms),
}

export const percentYAxisProps: YAxisProps = {
  domain: [0, 100],
  width: 36,
  tickLine: false,
  axisLine: false,
  tickFormatter: (value: number) => `${value}%`,
}
