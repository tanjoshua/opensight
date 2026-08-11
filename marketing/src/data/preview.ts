// Sample data behind every product preview on the site. It lives in one place
// so the homepage hero, the Monitor product card and the /monitor/ app mock can
// never drift into quoting different numbers for the same fictional business.
//
// Series are named generically ("You", "Competitor A"): a reader in any trade
// has to be able to see their own business here. The nine weekly runs rise and
// fall rather than ramping in a straight line, because that is the shape
// measured data has — a monotonic line reads as an illustration, not a record.

export const PREVIEW_DATES = [
  "May 25",
  "Jun 1",
  "Jun 8",
  "Jun 15",
  "Jun 22",
  "Jun 29",
  "Jul 6",
  "Jul 13",
  "Jul 20",
];

// The app labels ~5 ticks regardless of point count (trend-chart.tsx tickStep).
export const PREVIEW_TICKS = [0, 2, 4, 6, 8];

export const PREVIEW_SERIES = [
  {
    name: "You",
    color: "var(--color-series-you)",
    width: 2.5,
    values: [15, 21, 18, 28, 33, 31, 42, 49, 58],
  },
  {
    name: "Competitor A",
    color: "var(--color-series-2)",
    width: 2,
    values: [70, 66, 69, 62, 58, 60, 53, 48, 44],
  },
  {
    name: "Competitor B",
    color: "var(--color-series-3)",
    width: 2,
    values: [44, 47, 41, 43, 37, 34, 36, 30, 27],
  },
];

const you = PREVIEW_SERIES[0].values;

export const PREVIEW_LATEST_PERCENT = `${you[you.length - 1].toFixed(1)}%`;
export const PREVIEW_LATEST_DATE = "July 20, 2026";

/**
 * Maps the sample series onto an SVG box. `top`/`bottom` are the y coordinates
 * of 100% and 0%, matching the app's fixed 0-100% axis.
 */
export function plot(box: {
  left: number;
  right: number;
  top: number;
  bottom: number;
}) {
  const x = (i: number, count: number) =>
    box.left + (i * (box.right - box.left)) / (count - 1);
  const y = (percent: number) =>
    box.bottom - (percent / 100) * (box.bottom - box.top);
  const round = (n: number) => Number(n.toFixed(1));

  return {
    x: (i: number, count = PREVIEW_DATES.length) => round(x(i, count)),
    y: (percent: number) => round(y(percent)),
    path: (values: number[]) =>
      values
        .map(
          (value, i) =>
            `${i === 0 ? "M" : "L"}${round(x(i, values.length))} ${round(y(value))}`
        )
        .join(" "),
  };
}
