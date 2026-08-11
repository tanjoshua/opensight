import type { ChartConfig } from "@/components/ui/chart"

export const chartConfig = {
  you: { label: "You", color: "var(--chart-1)" },
} satisfies ChartConfig

// Visibility charts plot "You" against a few competitors, one series each,
// identified by color: --chart-1 (you) through --chart-4 (up to 3
// competitors), a fixed-order categorical set validated colorblind-safe as a
// line-chart adjacent-pair palette (dataviz skill). "You" stays visually
// dominant via line/dot weight, not color alone — a legend still names every
// line since two of the four slots (aqua, yellow) sit under 3:1 contrast on a
// light card.
export const COMPETITOR_COLORS = [
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
]
