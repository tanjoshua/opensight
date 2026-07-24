import { type ReactNode, useState } from "react"
import { ExternalLink, FileJson } from "lucide-react"
import { Link } from "react-router"

import {
  useResult,
  type ResultAnalysis,
  type ResultCitation,
  type ResultMention,
} from "@/api/responses"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"

export function ResponseDrawer({
  resultId,
  onOpenChange,
}: {
  resultId: string | undefined
  onOpenChange: (open: boolean) => void
}) {
  const [includeRaw, setIncludeRaw] = useState(false)
  const detail = useResult(resultId, { includeRaw })

  return (
    <Sheet
      open={resultId !== undefined}
      onOpenChange={(open) => {
        if (!open) setIncludeRaw(false)
        onOpenChange(open)
      }}
    >
      <SheetContent className="w-full overflow-y-auto sm:max-w-xl">
        <SheetHeader>
          <SheetTitle>Response detail</SheetTitle>
          <SheetDescription>
            Stored answer, analysis evidence, prompt, model, and run metadata.
          </SheetDescription>
        </SheetHeader>

        <div className="flex flex-col gap-5 px-6 pb-6">
          {detail.isLoading && <DrawerSkeleton />}
          {detail.isError && (
            <p className="text-sm text-destructive" role="alert">
              The response could not be loaded. Try again.
            </p>
          )}
          {detail.data && (
            <>
              <DetailSection title="Prompt">
                <p className="text-sm whitespace-pre-wrap">
                  {detail.data.prompt?.text ?? detail.data.prompt_id}
                </p>
              </DetailSection>

              <DetailSection
                title={
                  detail.data.status === "failed"
                    ? "Error"
                    : detail.data.unanalyzed
                      ? "Answer"
                      : "Answer Evidence"
                }
                action={
                  detail.data.unanalyzed ? (
                    <Badge variant="outline">not yet analyzed</Badge>
                  ) : undefined
                }
              >
                {detail.data.status === "failed" ? (
                  <p className="text-sm whitespace-pre-wrap text-destructive">
                    {detail.data.error ?? "Unknown error"}
                  </p>
                ) : detail.data.response_text ? (
                  <AnswerText
                    text={detail.data.response_text}
                    analysis={detail.data.analysis}
                  />
                ) : (
                  <p className="text-sm text-muted-foreground">
                    No answer text stored.
                  </p>
                )}
              </DetailSection>

              {detail.data.status !== "failed" && (
                <AnalysisSection
                  analysis={detail.data.analysis}
                  unanalyzed={detail.data.unanalyzed}
                />
              )}

              <DetailSection title="Run">
                <dl className="grid grid-cols-[7rem_1fr] gap-x-3 gap-y-2 text-sm">
                  <dt className="text-muted-foreground">Status</dt>
                  <dd>{detail.data.status}</dd>
                  <dt className="text-muted-foreground">Model</dt>
                  <dd>
                    {detail.data.model ?? "Not recorded"}{" "}
                    <Link
                      className="text-muted-foreground underline underline-offset-4 hover:text-foreground"
                      to="/methodology"
                    >
                      How measured
                    </Link>
                  </dd>
                  <dt className="text-muted-foreground">Requested</dt>
                  <dd>{formatDateTime(detail.data.requested_at)}</dd>
                  <dt className="text-muted-foreground">Completed</dt>
                  <dd>{formatDateTime(detail.data.completed_at)}</dd>
                  {detail.data.run && (
                    <>
                      <dt className="text-muted-foreground">Scheduled</dt>
                      <dd>{formatRunDate(detail.data.run.scheduled_for)}</dd>
                      <dt className="text-muted-foreground">Run status</dt>
                      <dd>{detail.data.run.status}</dd>
                    </>
                  )}
                </dl>
              </DetailSection>

              <DetailSection title="Request">
                <JSONBlock
                  value={detail.data.request}
                  empty="No request params stored."
                />
              </DetailSection>

              <Separator />

              <div className="flex flex-col gap-3">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="w-fit"
                  onClick={() => setIncludeRaw((value) => !value)}
                >
                  <FileJson data-icon="inline-start" />
                  {includeRaw ? "Hide raw JSON" : "Show raw JSON"}
                </Button>
                {includeRaw && (
                  <JSONBlock
                    value={detail.data.raw_response}
                    empty={
                      detail.isFetching
                        ? "Loading raw JSON."
                        : "No raw response stored."
                    }
                  />
                )}
              </div>
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

function DetailSection({
  title,
  action,
  children,
}: {
  title: string
  action?: ReactNode
  children: ReactNode
}) {
  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <h2 className="font-heading text-sm font-medium">{title}</h2>
        {action && <div className="ms-auto">{action}</div>}
      </div>
      {children}
    </section>
  )
}

function AnswerText({
  text,
  analysis,
}: {
  text: string
  analysis: ResultAnalysis | undefined
}) {
  const segments = buildAnswerSegments(text, analysis)
  return (
    <p className="text-sm leading-6 whitespace-pre-wrap">
      {segments.map((segment, index) => {
        const node =
          segment.text.length === 0 ? null : segment.mentions.length > 0 ? (
            <mark
              key={`text-${index}`}
              className={mentionHighlightClass(segment.mentions)}
              title={mentionTitle(segment.mentions)}
            >
              {segment.text}
            </mark>
          ) : segment.cited ? (
            <span
              key={`text-${index}`}
              className="border-b border-dotted border-primary/60"
            >
              {segment.text}
            </span>
          ) : (
            <span key={`text-${index}`}>{segment.text}</span>
          )
        return (
          <span key={index}>
            {node}
            {segment.markers.map((citation) => (
              <CitationMarker key={citation.cite_order} citation={citation} />
            ))}
          </span>
        )
      })}
    </p>
  )
}

function CitationMarker({ citation }: { citation: ResultCitation }) {
  const label = citation.cite_order + 1
  return (
    <sup className="ms-0.5 align-super text-[0.65rem] leading-none">
      <a
        href={`#citation-${citation.cite_order}`}
        className="rounded-sm bg-secondary px-1 py-0.5 font-medium text-secondary-foreground no-underline ring-1 ring-border hover:bg-muted"
        title={citation.title ?? citation.domain}
      >
        {label}
      </a>
    </sup>
  )
}

function AnalysisSection({
  analysis,
  unanalyzed,
}: {
  analysis: ResultAnalysis | undefined
  unanalyzed: boolean
}) {
  if (unanalyzed || analysis === undefined) {
    return (
      <DetailSection title="Analysis">
        <p className="text-sm text-muted-foreground">
          {unanalyzed
            ? "This succeeded response has not been analyzed yet."
            : "Analysis is not available for this response."}
        </p>
      </DetailSection>
    )
  }

  return (
    <DetailSection
      title="Analysis"
      action={
        analysis.sentiment ? (
          <Badge variant={sentimentVariant(analysis.sentiment)}>
            {analysis.sentiment}
          </Badge>
        ) : (
          <Badge variant="outline">no sentiment</Badge>
        )
      }
    >
      <div className="flex flex-col gap-4">
        <EvidenceGroup title="Keywords">
          {analysis.keywords.length === 0 ? (
            <EmptyEvidence>No keywords detected.</EmptyEvidence>
          ) : (
            <div className="flex flex-wrap gap-1.5">
              {analysis.keywords.map((keyword) => (
                <Badge key={keyword} variant="secondary">
                  {keyword}
                </Badge>
              ))}
            </div>
          )}
        </EvidenceGroup>

        <EvidenceGroup title="Supporting Excerpts">
          {analysis.excerpts.length === 0 ? (
            <EmptyEvidence>No supporting excerpts recorded.</EmptyEvidence>
          ) : (
            <ul className="flex flex-col gap-2">
              {analysis.excerpts.map((excerpt, index) => (
                <li
                  key={`${excerpt}-${index}`}
                  className="border-s-2 border-border ps-3 text-sm leading-6 text-muted-foreground"
                >
                  {excerpt}
                </li>
              ))}
            </ul>
          )}
        </EvidenceGroup>

        <EvidenceGroup title="Mentions">
          {analysis.mentions.length === 0 ? (
            <EmptyEvidence>
              No self or competitor mentions detected.
            </EmptyEvidence>
          ) : (
            <ul className="flex flex-col gap-2">
              {analysis.mentions.map((mention, index) => (
                <li
                  key={`${mention.subject}-${mention.order}-${index}`}
                  className="flex flex-col gap-1 rounded-md border px-3 py-2 text-sm"
                >
                  <span className="flex flex-wrap items-center gap-1.5">
                    <Badge
                      variant={
                        mention.subject === "self" ? "secondary" : "outline"
                      }
                    >
                      {subjectLabel(mention.subject)}
                    </Badge>
                    <span className="text-muted-foreground">
                      #{mention.order + 1} · {mention.matched_by}
                    </span>
                  </span>
                  <span className="font-medium">{mention.verbatim_name}</span>
                  {mention.excerpt && (
                    <span className="leading-6 text-muted-foreground">
                      {mention.excerpt}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </EvidenceGroup>

        <EvidenceGroup title="Citations">
          {analysis.citations.length === 0 ? (
            <EmptyEvidence>No citations recorded.</EmptyEvidence>
          ) : (
            <ul className="flex flex-col gap-2">
              {analysis.citations.map((citation) => (
                <li
                  id={`citation-${citation.cite_order}`}
                  key={`${citation.cite_order}-${citation.url}`}
                  className="scroll-mt-4 rounded-md border px-3 py-2 text-sm"
                >
                  <div className="flex flex-wrap items-center gap-1.5">
                    <Badge variant="secondary">{citation.cite_order + 1}</Badge>
                    <Badge variant="outline">
                      {subjectLabel(citation.subject)}
                    </Badge>
                    {citation.span === null && (
                      <Badge variant="outline">no span</Badge>
                    )}
                  </div>
                  <a
                    href={citation.url}
                    target="_blank"
                    rel="noreferrer"
                    className="mt-1.5 inline-flex max-w-full items-center gap-1.5 font-medium underline-offset-2 hover:underline"
                  >
                    <span className="truncate">
                      {citation.title || citation.domain}
                    </span>
                    <ExternalLink className="size-3.5 shrink-0" />
                  </a>
                  <p className="mt-1 truncate text-muted-foreground">
                    {citation.url}
                  </p>
                </li>
              ))}
            </ul>
          )}
        </EvidenceGroup>
      </div>
    </DetailSection>
  )
}

function EvidenceGroup({
  title,
  children,
}: {
  title: string
  children: ReactNode
}) {
  return (
    <div className="flex flex-col gap-2">
      <h3 className="text-xs font-medium tracking-normal text-muted-foreground">
        {title}
      </h3>
      {children}
    </div>
  )
}

function EmptyEvidence({ children }: { children: ReactNode }) {
  return <p className="text-sm text-muted-foreground">{children}</p>
}

interface AnswerSegment {
  text: string
  mentions: ResultMention[]
  cited: boolean
  markers: ResultCitation[]
}

interface MentionRange {
  start: number
  end: number
  mention: ResultMention
}

interface CitationRange {
  start: number
  end: number
  citation: ResultCitation
}

function buildAnswerSegments(
  text: string,
  analysis: ResultAnalysis | undefined
): AnswerSegment[] {
  const mentions = mentionRanges(text, analysis?.mentions ?? [])
  const citations = citationRanges(text, analysis?.citations ?? [])
  const boundaries = new Set<number>([0, text.length])
  for (const range of mentions) {
    boundaries.add(range.start)
    boundaries.add(range.end)
  }
  for (const range of citations) {
    boundaries.add(range.start)
    boundaries.add(range.end)
  }

  const ordered = [...boundaries].sort((a, b) => a - b)
  const markersByEnd = new Map<number, ResultCitation[]>()
  for (const range of citations) {
    const markers = markersByEnd.get(range.end) ?? []
    markers.push(range.citation)
    markersByEnd.set(range.end, markers)
  }

  const segments: AnswerSegment[] = []
  for (let i = 0; i < ordered.length - 1; i++) {
    const start = ordered[i]
    const end = ordered[i + 1]
    if (start === end) continue
    segments.push({
      text: text.slice(start, end),
      mentions: mentions
        .filter((range) => start >= range.start && start < range.end)
        .map((range) => range.mention),
      cited: citations.some(
        (range) => start >= range.start && start < range.end
      ),
      markers: markersByEnd.get(end)?.sort(byCiteOrder) ?? [],
    })
  }

  if (segments.length === 0) {
    const trailingMarkers = markersByEnd.get(text.length) ?? []
    return [
      {
        text,
        mentions: [],
        cited: false,
        markers: trailingMarkers.sort(byCiteOrder),
      },
    ]
  }
  return segments
}

function mentionRanges(
  text: string,
  mentions: ResultMention[]
): MentionRange[] {
  const ranges: MentionRange[] = []
  for (const mention of mentions) {
    const range =
      findTextRange(text, mention.verbatim_name) ??
      findTextRange(text, mention.excerpt)
    if (range !== null) {
      ranges.push({ ...range, mention })
    }
  }
  return ranges
    .filter((range) => range.end > range.start)
    .sort((a, b) => a.start - b.start || a.end - b.end)
}

function citationRanges(
  text: string,
  citations: ResultCitation[]
): CitationRange[] {
  const ranges: CitationRange[] = []
  for (const citation of citations) {
    if (citation.span === null) continue
    const start = citation.span.start
    const end = citation.span.end
    if (start < 0 || end <= start || start >= text.length) continue
    ranges.push({
      start,
      end: Math.min(end, text.length),
      citation,
    })
  }
  return ranges.sort(
    (a, b) => a.start - b.start || byCiteOrder(a.citation, b.citation)
  )
}

function findTextRange(
  text: string,
  needle: string
): { start: number; end: number } | null {
  const clean = needle.trim()
  if (clean.length === 0) return null

  const direct = text.indexOf(clean)
  if (direct >= 0) return { start: direct, end: direct + clean.length }

  const lowerText = text.toLowerCase()
  const lowerClean = clean.toLowerCase()
  const insensitive = lowerText.indexOf(lowerClean)
  if (insensitive >= 0) {
    return { start: insensitive, end: insensitive + clean.length }
  }

  const chunks = clean
    .split(/(?:\.{3}|…)/)
    .map((chunk) => chunk.trim())
    .filter((chunk) => chunk.length >= 3)
  if (chunks.length < 2) return null

  let cursor = 0
  let start = -1
  let end = -1
  for (const chunk of chunks) {
    const found = lowerText.indexOf(chunk.toLowerCase(), cursor)
    if (found < 0) return null
    if (start < 0) start = found
    end = found + chunk.length
    cursor = end
  }
  return start >= 0 && end > start ? { start, end } : null
}

function byCiteOrder(a: ResultCitation, b: ResultCitation): number {
  return a.cite_order - b.cite_order
}

function mentionHighlightClass(mentions: ResultMention[]): string {
  const subjects = new Set(mentions.map((mention) => mention.subject))
  if (subjects.size > 1) {
    return "rounded-sm bg-sky-100 px-0.5 text-sky-950 ring-1 ring-sky-200 dark:bg-sky-500/20 dark:text-sky-50 dark:ring-sky-500/30"
  }
  if (subjects.has("self")) {
    return "rounded-sm bg-emerald-100 px-0.5 text-emerald-950 ring-1 ring-emerald-200 dark:bg-emerald-500/20 dark:text-emerald-50 dark:ring-emerald-500/30"
  }
  return "rounded-sm bg-amber-100 px-0.5 text-amber-950 ring-1 ring-amber-200 dark:bg-amber-500/20 dark:text-amber-50 dark:ring-amber-500/30"
}

function mentionTitle(mentions: ResultMention[]): string {
  const labels = [
    ...new Set(mentions.map((mention) => subjectLabel(mention.subject))),
  ]
  return `${labels.join(" and ")} mention${labels.length === 1 ? "" : "s"}`
}

function sentimentVariant(
  sentiment: string
): "secondary" | "outline" | "destructive" {
  return sentiment === "negative"
    ? "destructive"
    : sentiment === "positive"
      ? "secondary"
      : "outline"
}

function subjectLabel(subject: string): string {
  switch (subject) {
    case "self":
      return "Self"
    case "business":
      return "Business"
    case "competitor":
      return "Competitor"
    case "other":
      return "Other"
    case "unknown":
      return "Unknown"
    default:
      return subject
  }
}

function JSONBlock({ value, empty }: { value: unknown; empty: string }) {
  if (value === undefined || value === null || value === "") {
    return <p className="text-sm text-muted-foreground">{empty}</p>
  }
  return (
    <pre className="max-h-80 overflow-auto rounded-lg bg-muted p-3 text-xs leading-5">
      {JSON.stringify(value, null, 2)}
    </pre>
  )
}

function DrawerSkeleton() {
  return (
    <div className="flex flex-col gap-4">
      <Skeleton className="h-4 w-24" />
      <Skeleton className="h-20 w-full" />
      <Skeleton className="h-4 w-20" />
      <Skeleton className="h-32 w-full" />
      <Skeleton className="h-24 w-full" />
    </div>
  )
}

function formatDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.valueOf())) return value
  return date.toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  })
}

function formatRunDate(value: string): string {
  const date = new Date(`${value}T00:00:00`)
  if (Number.isNaN(date.valueOf())) return value
  return date.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  })
}
