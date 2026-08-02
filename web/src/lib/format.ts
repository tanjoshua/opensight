// Shared value formatting for the SPA. `scheduled_for` fields are plain dates
// (YYYY-MM-DD); every parse here reads them as local midnight rather than UTC,
// so trend ticks and run dates land on the intended day.

export function formatPercent(value: number): string {
  return `${(Math.round(value * 10) / 10).toFixed(1)}%`
}

export function pluralize(count: number, singular: string): string {
  return count === 1 ? singular : `${singular}s`
}

export function dateMs(scheduledFor: string): number {
  return dateOnly(scheduledFor).getTime()
}

// Month and day only ("Feb 3") — trend axis ticks and dated run buttons.
export function shortDate(ms: number): string {
  return new Date(ms).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  })
}

// Spelled-out date ("February 3, 2026") — headline dates on the Overview.
export function longDate(scheduledFor: string): string {
  return dateOnly(scheduledFor).toLocaleDateString(undefined, {
    year: "numeric",
    month: "long",
    day: "numeric",
  })
}

// Abbreviated date with year ("Feb 3, 2026") — run dates.
export function formatDateOnly(date: Date): string {
  return date.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  })
}

export function formatRunDate(scheduledFor: string): string {
  return formatDateOnly(dateOnly(scheduledFor))
}

function dateOnly(scheduledFor: string): Date {
  return new Date(`${scheduledFor}T00:00:00`)
}
