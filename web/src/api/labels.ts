// Label maps for the six domain enums that get rendered as text anywhere in
// the UI. tsc cannot catch a numeric enum value leaking into a React child or
// template literal, so every render site must go through one of these instead
// of interpolating the enum directly (RPC-7). Typed Record<number, string>,
// not Record<SpecificEnum, string>: the generated enum type includes an
// "unknown enum value" branch that a keyed Record can't express.
import {
  CitationSubject,
  CompetitorStatus,
  MatchMethod,
  MentionSubject,
  PromptStatus,
  ResultStatus,
  RunStatus,
  RunTrigger,
  Sentiment,
} from "@/gen/opensight/v1/common_pb"

const runStatusLabels: Record<number, string> = {
  [RunStatus.RUNNING]: "running",
  [RunStatus.COMPLETED]: "completed",
  [RunStatus.PARTIAL]: "partial",
  [RunStatus.FAILED]: "failed",
}
export function runStatusLabel(status: RunStatus): string {
  return runStatusLabels[status] ?? "unknown"
}

// runTriggerLabel is only rendered for INITIAL/MANUAL — SCHEDULED shows no
// badge at all (RUNS-4: a scheduled run is the unmarked default, so calling
// this out would be noise on every row).
const runTriggerLabels: Record<number, string> = {
  [RunTrigger.INITIAL]: "First run",
  [RunTrigger.MANUAL]: "Manual",
}
export function runTriggerLabel(trigger: RunTrigger): string | undefined {
  return runTriggerLabels[trigger]
}

const resultStatusLabels: Record<number, string> = {
  [ResultStatus.SUCCEEDED]: "succeeded",
  [ResultStatus.FAILED]: "failed",
}
export function resultStatusLabel(status: ResultStatus): string {
  return resultStatusLabels[status] ?? "unknown"
}

const sentimentLabels: Record<number, string> = {
  [Sentiment.POSITIVE]: "positive",
  [Sentiment.NEUTRAL]: "neutral",
  [Sentiment.NEGATIVE]: "negative",
  [Sentiment.MIXED]: "mixed",
}
export function sentimentLabel(sentiment: Sentiment): string {
  return sentimentLabels[sentiment] ?? ""
}

const promptStatusLabels: Record<number, string> = {
  [PromptStatus.ACTIVE]: "active",
  [PromptStatus.RETIRED]: "retired",
}
export function promptStatusLabel(status: PromptStatus): string {
  return promptStatusLabels[status] ?? "unknown"
}

const matchMethodLabels: Record<number, string> = {
  [MatchMethod.EXACT]: "exact",
  [MatchMethod.LLM]: "llm",
}
export function matchMethodLabel(method: MatchMethod): string {
  return matchMethodLabels[method] ?? "unknown"
}

const mentionSubjectLabels: Record<number, string> = {
  [MentionSubject.SELF]: "Self",
  [MentionSubject.COMPETITOR]: "Competitor",
}
export function mentionSubjectLabel(subject: MentionSubject): string {
  return mentionSubjectLabels[subject] ?? "Unknown"
}

// competitorStatusLabel round-trips the Competitors page's ?status= URL param
// (discovered|tracked|dismissed) the same way resultStatusLabel does for run
// detail — see competitors-page.tsx's statusParam.
const competitorStatusLabels: Record<number, string> = {
  [CompetitorStatus.DISCOVERED]: "discovered",
  [CompetitorStatus.TRACKED]: "tracked",
  [CompetitorStatus.DISMISSED]: "dismissed",
}
export function competitorStatusLabel(status: CompetitorStatus): string {
  return competitorStatusLabels[status] ?? "unknown"
}

const citationSubjectLabels: Record<number, string> = {
  [CitationSubject.BUSINESS]: "Business",
  [CitationSubject.COMPETITOR]: "Competitor",
  [CitationSubject.OTHER]: "Other",
  [CitationSubject.UNKNOWN]: "Unknown",
}
export function citationSubjectLabel(subject: CitationSubject): string {
  return citationSubjectLabels[subject] ?? "Unknown"
}

// stripeStatusLabel maps GetBillingResponse.stripe_status — VERBATIM Stripe
// text, never a locally-invented value (design 08) — to display copy. Unlike
// the maps above this is keyed by string, not a generated enum: stripe_status
// is deliberately a plain string on the wire (billing.proto), since the set
// of Stripe statuses isn't ours to close. Empty string covers "no
// subscription" (never paid, or comped).
const stripeStatusLabels: Record<string, string> = {
  active: "Active",
  trialing: "Trial",
  past_due: "Payment past due",
  canceled: "Canceled",
  unpaid: "Unpaid",
  incomplete: "Incomplete",
  incomplete_expired: "Incomplete (expired)",
  paused: "Paused",
}
export function stripeStatusLabel(status: string): string {
  return stripeStatusLabels[status] ?? (status || "No subscription")
}
