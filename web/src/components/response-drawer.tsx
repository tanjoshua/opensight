import { skipToken, useQuery } from "@connectrpc/connect-query"
import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt"
import { type ReactNode, useMemo, useState } from "react"
import { ChevronLeft, ChevronRight, ExternalLink, FileJson } from "lucide-react"
import { Link } from "react-router"

import {
  citationSubjectLabel,
  matchMethodLabel,
  mentionSubjectLabel,
  resultStatusLabel,
  runStatusLabel,
  sentimentLabel,
} from "@/api/labels"
import {
  MentionSubject,
  ResultStatus,
  Sentiment,
} from "@/gen/opensight/v1/common_pb"
import type {
  ResultAnalysis,
  ResultCitation,
  ResultMention,
} from "@/gen/opensight/v1/result_pb"
import { getResult } from "@/gen/opensight/v1/result-ResultService_connectquery"
import {
  dedupeResultIds,
  type EvidenceSelection,
} from "@/components/evidence-selection"
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
import { formatRunDate } from "@/lib/format"

export function ResponseDrawer({
  evidence,
  onOpenChange,
}: {
  evidence: EvidenceSelection | undefined
  onOpenChange: (open: boolean) => void
}) {
  const resultIds = useMemo(
    () => dedupeResultIds(evidence?.resultIds ?? []),
    [evidence]
  )
  const selectionKey = `${evidence?.context ?? ""}\u0000${resultIds.join("\u0000")}`

  return (
    <ResponseDrawerContent
      key={selectionKey}
      evidence={evidence}
      resultIds={resultIds}
      onOpenChange={onOpenChange}
    />
  )
}

function ResponseDrawerContent({
  evidence,
  resultIds,
  onOpenChange,
}: {
  evidence: EvidenceSelection | undefined
  resultIds: string[]
  onOpenChange: (open: boolean) => void
}) {
  const [currentIndex, setCurrentIndex] = useState(0)
  const [includeRaw, setIncludeRaw] = useState(false)
  const resultId = resultIds[currentIndex]

  const detail = useQuery(
    getResult,
    resultId === undefined ? skipToken : { resultId, includeRaw }
  )
  const result = detail.data?.result
  const count = resultIds.length
  const hasPrevious = currentIndex > 0
  const hasNext = currentIndex + 1 < count

  return (
    <Sheet
      open={evidence !== undefined && resultId !== undefined}
      onOpenChange={(open) => {
        if (!open) setIncludeRaw(false)
        onOpenChange(open)
      }}
    >
      <SheetContent className="w-full overflow-y-auto sm:max-w-2xl">
        <SheetHeader className="border-b">
          <SheetTitle>Evidence</SheetTitle>
          <SheetDescription>
            {evidence?.context ?? "Stored response and analysis evidence."}
          </SheetDescription>
          {count > 1 && (
            <div
              className="mt-2 flex items-center gap-2"
              aria-label="Evidence response navigation"
            >
              <span
                className="me-auto text-sm font-medium tabular-nums"
                aria-live="polite"
              >
                Response {currentIndex + 1} of {count}
              </span>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="min-h-11"
                disabled={!hasPrevious}
                onClick={() => setCurrentIndex((index) => index - 1)}
              >
                <ChevronLeft data-icon="inline-start" />
                Previous
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="min-h-11"
                disabled={!hasNext}
                onClick={() => setCurrentIndex((index) => index + 1)}
              >
                Next
                <ChevronRight data-icon="inline-end" />
              </Button>
            </div>
          )}
        </SheetHeader>

        <div className="flex flex-col gap-5 px-6 pb-6">
          {detail.isLoading && <DrawerSkeleton />}
          {detail.isError && (
            <p className="text-sm text-destructive" role="alert">
              The response could not be loaded. Try again.
            </p>
          )}
          {result && (
            <>
              <DetailSection
                title={
                  result.status === ResultStatus.FAILED
                    ? "Error"
                    : result.unanalyzed
                      ? "Answer"
                      : "Answer Evidence"
                }
                action={
                  result.unanalyzed ? (
                    <Badge variant="outline">not yet analyzed</Badge>
                  ) : undefined
                }
              >
                {result.status === ResultStatus.FAILED ? (
                  <p className="text-sm whitespace-pre-wrap text-destructive">
                    {result.error ?? "Unknown error"}
                  </p>
                ) : result.responseText ? (
                  <AnswerText
                    text={result.responseText}
                    analysis={result.analysis}
                  />
                ) : (
                  <p className="text-sm text-muted-foreground">
                    No answer text stored.
                  </p>
                )}
              </DetailSection>

              {result.status !== ResultStatus.FAILED && (
                <AnalysisSection
                  analysis={result.analysis}
                  unanalyzed={result.unanalyzed}
                />
              )}

              <DetailSection title="Prompt">
                <p className="text-sm whitespace-pre-wrap">
                  {result.prompt?.text ?? result.promptId}
                </p>
              </DetailSection>

              <Separator />
              <details className="group rounded-lg border">
                <summary className="cursor-pointer px-4 py-3 text-sm font-medium select-none">
                  Technical details
                </summary>
                <div className="flex flex-col gap-5 border-t px-4 py-4">
                  <DetailSection title="Run">
                    <dl className="grid grid-cols-[7rem_1fr] gap-x-3 gap-y-2 text-sm">
                      <dt className="text-muted-foreground">Status</dt>
                      <dd>{resultStatusLabel(result.status)}</dd>
                      <dt className="text-muted-foreground">Model</dt>
                      <dd>
                        {result.model ?? "Not recorded"}{" "}
                        <Link
                          className="text-muted-foreground underline underline-offset-4 hover:text-foreground"
                          to="/methodology"
                        >
                          How measured
                        </Link>
                      </dd>
                      <dt className="text-muted-foreground">Requested</dt>
                      <dd>{formatDateTime(result.requestedAt)}</dd>
                      <dt className="text-muted-foreground">Completed</dt>
                      <dd>{formatDateTime(result.completedAt)}</dd>
                      {result.run && (
                        <>
                          <dt className="text-muted-foreground">Scheduled</dt>
                          <dd>{formatRunDate(result.run.scheduledFor)}</dd>
                          <dt className="text-muted-foreground">Run status</dt>
                          <dd>{runStatusLabel(result.run.status)}</dd>
                        </>
                      )}
                    </dl>
                  </DetailSection>
                  <DetailSection title="Request">
                    <JSONBlock
                      json={result.requestJson}
                      empty="No request params stored."
                    />
                  </DetailSection>
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
                      json={result.rawResponseJson}
                      empty={
                        detail.isFetching
                          ? "Loading raw JSON."
                          : "No raw response stored."
                      }
                    />
                  )}
                </div>
              </details>
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
              <CitationMarker key={citation.citeOrder} citation={citation} />
            ))}
          </span>
        )
      })}
    </p>
  )
}

function CitationMarker({ citation }: { citation: ResultCitation }) {
  const label = citation.citeOrder + 1
  return (
    <sup className="ms-0.5 align-super text-[0.65rem] leading-none">
      <a
        href={`#citation-${citation.citeOrder}`}
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
        analysis.sentiment !== Sentiment.UNSPECIFIED ? (
          <Badge variant={sentimentVariant(analysis.sentiment)}>
            {sentimentLabel(analysis.sentiment)}
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
                        mention.subject === MentionSubject.SELF
                          ? "secondary"
                          : "outline"
                      }
                    >
                      {mentionSubjectLabel(mention.subject)}
                    </Badge>
                    <span className="text-muted-foreground">
                      #{mention.order + 1} ·{" "}
                      {matchMethodLabel(mention.matchedBy)}
                    </span>
                  </span>
                  <span className="font-medium">{mention.verbatimName}</span>
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
                  id={`citation-${citation.citeOrder}`}
                  key={`${citation.citeOrder}-${citation.url}`}
                  className="scroll-mt-4 rounded-md border px-3 py-2 text-sm"
                >
                  <div className="flex flex-wrap items-center gap-1.5">
                    <Badge variant="secondary">{citation.citeOrder + 1}</Badge>
                    <Badge variant="outline">
                      {citationSubjectLabel(citation.subject)}
                    </Badge>
                    {citation.span === undefined && (
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
      findTextRange(text, mention.verbatimName) ??
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
    if (citation.span === undefined) continue
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
  return a.citeOrder - b.citeOrder
}

function mentionHighlightClass(mentions: ResultMention[]): string {
  const subjects = new Set(mentions.map((mention) => mention.subject))
  if (subjects.size > 1) {
    return "rounded-sm bg-sky-100 px-0.5 text-sky-950 ring-1 ring-sky-200 dark:bg-sky-500/20 dark:text-sky-50 dark:ring-sky-500/30"
  }
  if (subjects.has(MentionSubject.SELF)) {
    return "rounded-sm bg-emerald-100 px-0.5 text-emerald-950 ring-1 ring-emerald-200 dark:bg-emerald-500/20 dark:text-emerald-50 dark:ring-emerald-500/30"
  }
  return "rounded-sm bg-amber-100 px-0.5 text-amber-950 ring-1 ring-amber-200 dark:bg-amber-500/20 dark:text-amber-50 dark:ring-amber-500/30"
}

function mentionTitle(mentions: ResultMention[]): string {
  const labels = [
    ...new Set(mentions.map((mention) => mentionSubjectLabel(mention.subject))),
  ]
  return `${labels.join(" and ")} mention${labels.length === 1 ? "" : "s"}`
}

function sentimentVariant(
  sentiment: Sentiment
): "secondary" | "outline" | "destructive" {
  return sentiment === Sentiment.NEGATIVE
    ? "destructive"
    : sentiment === Sentiment.POSITIVE
      ? "secondary"
      : "outline"
}

function JSONBlock({ json, empty }: { json: string; empty: string }) {
  if (json === "") {
    return <p className="text-sm text-muted-foreground">{empty}</p>
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(json)
  } catch {
    parsed = json
  }
  return (
    <pre className="max-h-80 overflow-auto rounded-lg bg-muted p-3 text-xs leading-5">
      {JSON.stringify(parsed, null, 2)}
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

function formatDateTime(value: Timestamp | undefined): string {
  if (value === undefined) return "-"
  const date = timestampDate(value)
  if (Number.isNaN(date.valueOf())) return "-"
  return date.toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  })
}
