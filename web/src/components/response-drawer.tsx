import { skipToken, useQuery } from "@connectrpc/connect-query"
import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt"
import { type ReactNode, useMemo, useState } from "react"
import {
  ArrowRight,
  ChevronLeft,
  ChevronRight,
  ExternalLink,
  FileJson,
} from "lucide-react"
import { Link } from "react-router"
import { useAccountPath } from "@/lib/account-path"

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
import type { ResultAnalysis } from "@/gen/opensight/v1/result_pb"
import { getResult } from "@/gen/opensight/v1/result-ResultService_connectquery"
import { AnswerText } from "@/components/answer-text"
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
  const path = useAccountPath()
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

              <DetailSection title="Question">
                <p className="text-sm whitespace-pre-wrap">
                  {result.prompt?.text ?? result.promptId}
                </p>
                {/* The drawer is for inspecting one response from somewhere
                    else in the app. Reading the whole answer, or comparing it
                    across runs, belongs on the question page. */}
                <Link
                  to={path(
                    `/prompts/${result.promptId}?run=${encodeURIComponent(result.runId)}`
                  )}
                  className="inline-flex w-fit items-center gap-1 text-sm font-medium underline underline-offset-4"
                >
                  Open full answer
                  <ArrowRight className="size-3.5" />
                </Link>
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
                          to={path("/methodology")}
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
        {/* Keywords are extracted phrases, not tags — long enough to overflow a
            non-wrapping Badge, so they are listed rather than pilled. */}
        <EvidenceGroup title="Keywords">
          {analysis.keywords.length === 0 ? (
            <EmptyEvidence>No keywords detected.</EmptyEvidence>
          ) : (
            <ul className="flex flex-col gap-1.5">
              {analysis.keywords.map((keyword) => (
                <li
                  key={keyword}
                  className="border-s-2 border-border ps-3 text-sm leading-6 text-muted-foreground"
                >
                  {keyword}
                </li>
              ))}
            </ul>
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
