// Question detail (INS-2, design 06): the reading view for one monitored
// question. The answer is a document and the analysis is annotation on it, so
// they sit side by side — a measured answer column with the model's markdown
// rendered, mentions highlighted and citations numbered, beside a sticky rail
// that indexes the same evidence. Selecting a run swaps the answer and is
// carried in ?run=, so a specific answer is linkable.
//
// This page replaced a result-history table whose rows opened the Response
// drawer. The drawer is still the right tool from other sections (a stat there
// is inspected in place and returned from); it was the wrong tool here, where
// reading the answer is the whole task.
import {
  createConnectQueryKey,
  skipToken,
  useMutation,
  useQuery,
} from "@connectrpc/connect-query"
import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt"
import { useQueryClient } from "@tanstack/react-query"
import {
  ArrowLeft,
  ExternalLink,
  MessageSquareText,
  Replace,
} from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"

import { useAccountNavigate, useAccountPath } from "@/lib/account-path"

import { errorMessage } from "@/api/errors"
import { useCurrentBusiness } from "@/api/hooks"
import {
  citationSubjectLabel,
  matchMethodLabel,
  promptStatusLabel,
  resultStatusLabel,
  runStatusLabel,
  sentimentLabel,
} from "@/api/labels"
import {
  MentionSubject,
  PromptStatus,
  ResultStatus,
  Sentiment,
} from "@/gen/opensight/v1/common_pb"
import type { Prompt } from "@/gen/opensight/v1/prompt_pb"
import type {
  PromptResult,
  ResultAnalysis,
} from "@/gen/opensight/v1/result_pb"
import {
  getPrompt,
  listPrompts,
  replacePrompt,
} from "@/gen/opensight/v1/prompt-PromptService_connectquery"
import { getResult } from "@/gen/opensight/v1/result-ResultService_connectquery"
import { AnswerText, type AnswerHighlight } from "@/components/answer-text"
import { PromptConfirmDialog } from "@/components/prompt-confirm-dialog"
import { SectionMessage } from "@/components/section-message"
import { mentionAnchorIndices } from "@/lib/answer-ranges"
import { formatDateOnly, formatRunDate } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"

export function PromptDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useAccountNavigate()
  const path = useAccountPath()
  const { businessId } = useCurrentBusiness()
  const queryClient = useQueryClient()
  const [searchParams, setSearchParams] = useSearchParams()
  const [replaceOpen, setReplaceOpen] = useState(false)

  const promptQuery = useQuery(
    getPrompt,
    id === undefined ? skipToken : { promptId: id }
  )
  const replacePromptMutation = useMutation(replacePrompt, {
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: listPrompts,
          input: businessId === undefined ? undefined : { businessId },
          cardinality: "finite",
        }),
      })
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: getPrompt,
          input: { promptId: variables.promptId },
          cardinality: "finite",
        }),
      })
    },
  })

  // GetPrompt returns results newest-first; the run strip reads oldest-first so
  // time runs left to right, matching the Questions table's sparkline.
  const runs = useMemo(
    () => [...(promptQuery.data?.results ?? [])].reverse(),
    [promptQuery.data]
  )
  const requestedRun = searchParams.get("run")
  const selected =
    runs.find((result) => result.runId === requestedRun) ??
    runs[runs.length - 1]

  const selectRun = (runId: string) => {
    const next = new URLSearchParams(searchParams)
    next.set("run", runId)
    // replace, not push: stepping through a year of runs should not bury the
    // Questions list under fifty history entries.
    setSearchParams(next, { replace: true })
  }

  if (promptQuery.isError) {
    return (
      <SectionMessage
        icon={MessageSquareText}
        title="Question not found"
        description="This question could not be loaded. It may have been removed."
      />
    )
  }
  if (!promptQuery.data) {
    return <DetailSkeleton />
  }
  const { prompt, lineage } = promptQuery.data
  if (!prompt) {
    return (
      <SectionMessage
        icon={MessageSquareText}
        title="Question not found"
        description="This question could not be loaded. It may have been removed."
      />
    )
  }
  const replacements = buildReplacements(prompt, lineage)

  const replaceError = replacePromptMutation.isError
    ? errorMessage(
        replacePromptMutation.error,
        "Could not replace question. Try again."
      )
    : undefined

  const openReplace = (open: boolean) => {
    setReplaceOpen(open)
    if (!open) replacePromptMutation.reset()
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-3">
        <div className="flex items-center gap-2">
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Back to questions"
            onClick={() => navigate("/prompts")}
          >
            <ArrowLeft />
          </Button>
          <span className="text-sm text-muted-foreground">Question</span>
          {prompt.status === PromptStatus.RETIRED && (
            <Badge variant="outline" className="capitalize">
              {promptStatusLabel(prompt.status)}
            </Badge>
          )}
          {prompt.status === PromptStatus.ACTIVE && (
            <Button
              variant="outline"
              size="sm"
              className="ms-auto"
              onClick={() => openReplace(true)}
            >
              <Replace data-icon="inline-start" />
              Replace
            </Button>
          )}
        </div>
        {/* The question is the page's title. Wrapping it in a card under a
            "Prompt" heading made the reader's own words look like metadata. */}
        <h1 className="max-w-[52ch] font-heading text-xl leading-8 font-semibold text-balance">
          {prompt.text}
        </h1>
        {replacements.length > 0 && (
          <div className="flex flex-col gap-1 text-sm text-muted-foreground">
            {replacements.map((r) => (
              <div key={r.replacedId}>
                Replaced{" "}
                <Link
                  to={path(`/prompts/${r.replacedId}`)}
                  className="text-foreground underline underline-offset-2"
                >
                  “{r.replacedText}”
                </Link>{" "}
                on {formatDate(r.on)}
              </div>
            ))}
          </div>
        )}
      </div>

      <PromptConfirmDialog
        mode="replace"
        open={replaceOpen}
        onOpenChange={openReplace}
        initialText={prompt.text}
        submitting={replacePromptMutation.isPending}
        errorMessage={replaceError}
        onSubmit={(text) =>
          replacePromptMutation.mutate(
            { promptId: prompt.id, text, confirmed: true },
            {
              onSuccess: (data) => {
                setReplaceOpen(false)
                if (data.prompt) navigate(`/prompts/${data.prompt.id}`)
              },
            }
          )
        }
      />

      {selected === undefined ? (
        <SectionMessage
          icon={MessageSquareText}
          title="No responses yet"
          description="This question has not been asked in a monitoring run yet. Its first answer appears here once a run completes."
        />
      ) : (
        <>
          <RunStrip
            runs={runs}
            selectedId={selected.id}
            onSelect={selectRun}
          />
          <Answer key={selected.id} resultId={selected.id} />
        </>
      )}
    </div>
  )
}

// RunStrip replaces the old result-history table. One run is one dated pill
// carrying its own outcome, which is the entire content the table's rows had —
// a table of one truncated preview column was a table pretending to have
// something to show.
function RunStrip({
  runs,
  selectedId,
  onSelect,
}: {
  runs: PromptResult[]
  selectedId: string
  onSelect: (runId: string) => void
}) {
  const selectedRef = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    selectedRef.current?.scrollIntoView({ block: "nearest", inline: "center" })
  }, [selectedId])

  if (runs.length === 0) return null
  return (
    <div className="flex flex-col gap-2">
      <h2 className="text-sm font-medium text-muted-foreground">
        {runs.length === 1
          ? "1 response"
          : `${runs.length} responses, oldest first`}
      </h2>
      <div
        className="-mx-1 flex gap-1.5 overflow-x-auto px-1 pb-1"
        role="group"
        aria-label="Select a monitoring run"
      >
        {runs.map((run) => {
          const active = run.id === selectedId
          return (
            <button
              key={run.id}
              ref={active ? selectedRef : undefined}
              type="button"
              aria-current={active}
              onClick={() => onSelect(run.runId)}
              className={cn(
                "flex min-h-11 shrink-0 items-center gap-2 rounded-full border px-3.5 text-sm whitespace-nowrap transition-colors",
                active
                  ? "border-foreground/25 bg-background font-medium shadow-sm"
                  : "border-transparent bg-muted/60 text-muted-foreground hover:bg-muted"
              )}
            >
              <OutcomeDot result={run} />
              {formatDate(run.requestedAt)}
            </button>
          )
        })}
      </div>
    </div>
  )
}

// OutcomeDot is the scanning affordance the sparkline gives on the Questions
// table, carried onto this page: filled where the business was mentioned,
// hollow where it was measured and absent, and visibly distinct for the two
// states that are neither (failed, not yet analyzed).
function OutcomeDot({ result }: { result: PromptResult }) {
  if (result.status === ResultStatus.FAILED) {
    return (
      <span
        aria-hidden
        className="block size-2.5 rotate-45 bg-destructive"
        title="Failed"
      />
    )
  }
  if (result.mentioned === undefined) {
    return (
      <span
        aria-hidden
        className="block size-2.5 rounded-full border border-dashed border-muted-foreground/60"
        title="Not yet analyzed"
      />
    )
  }
  return (
    <span
      aria-hidden
      className={cn(
        "block size-2.5 rounded-full",
        result.mentioned
          ? "bg-primary"
          : "border border-muted-foreground/60"
      )}
      title={result.mentioned ? "Mentioned" : "Not mentioned"}
    />
  )
}

function Answer({ resultId }: { resultId: string }) {
  const [includeRaw, setIncludeRaw] = useState(false)
  const [active, setActive] = useState<AnswerHighlight>()
  const detail = useQuery(getResult, { resultId, includeRaw })
  const result = detail.data?.result

  // The flash is a one-shot cue that the rail and the answer are the same
  // evidence; leaving it on would just be a second, competing highlight.
  useEffect(() => {
    if (active === undefined) return
    const timer = setTimeout(() => setActive(undefined), 1400)
    return () => clearTimeout(timer)
  }, [active])

  const reveal = (highlight: AnswerHighlight, anchor: string) => {
    document
      .getElementById(anchor)
      ?.scrollIntoView({ block: "center", behavior: "smooth" })
    setActive(highlight)
  }

  if (detail.isError) {
    return (
      <p className="text-sm text-destructive" role="alert">
        This response could not be loaded. Try again.
      </p>
    )
  }
  if (!result) return <AnswerSkeleton />

  if (result.status === ResultStatus.FAILED) {
    return (
      <div className="flex max-w-[68ch] flex-col gap-2 rounded-lg border border-destructive/40 bg-destructive/5 p-4">
        <h2 className="font-heading text-sm font-medium">
          This run failed to collect an answer
        </h2>
        <p className="text-sm whitespace-pre-wrap text-destructive">
          {result.error ?? "Unknown error"}
        </p>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-8">
      {/* The answer track is capped rather than fluid: at 14px this holds a
          ~80-character line, and it keeps the rail beside the prose instead of
          stranded across a wide gap. `ch` is no use here — it resolves against
          the wrapper's font size, not the text's. */}
      <div className="grid items-start gap-10 lg:grid-cols-[minmax(0,32rem)_20rem]">
        <div className="flex min-w-0 flex-col gap-6">
          {result.unanalyzed && (
            <p className="rounded-lg border border-dashed px-4 py-3 text-sm text-muted-foreground">
              This answer arrived but has not been analyzed yet, so it carries
              no highlights or evidence.
            </p>
          )}
          {result.responseText ? (
            <AnswerText
              text={result.responseText}
              analysis={result.analysis}
              active={active}
            />
          ) : (
            <p className="text-sm text-muted-foreground">
              No answer text stored.
            </p>
          )}
        </div>

        {/* Sticky, because the point of the rail is to stay legible while the
            answer scrolls past it. */}
        {/* min-w-0 so an unbreakable child can never widen the track and turn
            the rail's vertical scroll into a horizontal one. */}
        <aside className="top-6 flex min-w-0 flex-col gap-5 lg:sticky lg:max-h-[calc(100vh-3rem)] lg:overflow-y-auto lg:pe-1">
          <AnalysisRail
            text={result.responseText ?? ""}
            analysis={result.analysis}
            unanalyzed={result.unanalyzed}
            onReveal={reveal}
          />
        </aside>
      </div>

      {/* Full width, below the reading columns: stored JSON is data to inspect,
          not prose to read, and cramming it into the measure helps no one. */}
      <Separator />
      <TechnicalDetails
        result={result}
        includeRaw={includeRaw}
        onToggleRaw={() => setIncludeRaw((value) => !value)}
        fetchingRaw={detail.isFetching}
      />
    </div>
  )
}

function AnalysisRail({
  text,
  analysis,
  unanalyzed,
  onReveal,
}: {
  text: string
  analysis: ResultAnalysis | undefined
  unanalyzed: boolean
  onReveal: (highlight: AnswerHighlight, anchor: string) => void
}) {
  const anchors = useMemo(
    () => mentionAnchorIndices(text, analysis),
    [text, analysis]
  )

  if (unanalyzed || analysis === undefined) {
    return (
      <RailGroup title="Analysis">
        <p className="text-sm text-muted-foreground">
          {unanalyzed
            ? "Not analyzed yet."
            : "Analysis is not available for this response."}
        </p>
      </RailGroup>
    )
  }

  const self = analysis.mentions.filter(
    (mention) => mention.subject === MentionSubject.SELF
  )
  const others = analysis.mentions
    .map((mention, index) => ({ mention, index }))
    .filter(({ mention }) => mention.subject !== MentionSubject.SELF)
  const selfIndex = analysis.mentions.findIndex(
    (mention) => mention.subject === MentionSubject.SELF
  )

  return (
    <>
      <div className="flex flex-col gap-3 rounded-lg border p-4">
        <div className="flex flex-col gap-1">
          <span className="text-xs text-muted-foreground">Your business</span>
          <span className="font-heading text-lg font-semibold">
            {self.length > 0
              ? `Mentioned ${ordinal(self[0].order)}`
              : "Not mentioned"}
          </span>
        </div>
        {analysis.sentiment !== Sentiment.UNSPECIFIED && (
          <div className="flex items-center gap-2 text-sm">
            <span className="text-muted-foreground">Sentiment</span>
            <Badge variant={sentimentVariant(analysis.sentiment)}>
              {sentimentLabel(analysis.sentiment)}
            </Badge>
          </div>
        )}
        {self.length > 0 && anchors.has(selfIndex) && (
          <button
            type="button"
            className="w-fit text-sm underline underline-offset-4 hover:text-foreground"
            onClick={() =>
              onReveal(
                { kind: "mention", index: selfIndex },
                `mention-${selfIndex}`
              )
            }
          >
            Find it in the answer
          </button>
        )}
        {analysis.excerpts.length > 0 && (
          <ul className="flex flex-col gap-1.5 border-t pt-3">
            {analysis.excerpts.map((excerpt, index) => (
              <li
                key={`${excerpt}-${index}`}
                className="border-s-2 border-border ps-2.5 text-xs leading-5 text-muted-foreground"
              >
                {excerpt}
              </li>
            ))}
          </ul>
        )}
      </div>

      <RailGroup
        title={`Also in this answer${others.length > 0 ? ` (${others.length})` : ""}`}
      >
        {others.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No competitors were named.
          </p>
        ) : (
          <ol className="flex flex-col">
            {others.map(({ mention, index }) => {
              const findable = anchors.has(index)
              const body = (
                <>
                  <span className="w-5 shrink-0 text-xs tabular-nums text-muted-foreground">
                    {mention.order + 1}
                  </span>
                  <span className="min-w-0 flex-1 truncate">
                    {mention.verbatimName}
                  </span>
                  <span className="shrink-0 text-xs text-muted-foreground">
                    {matchMethodLabel(mention.matchedBy)}
                  </span>
                </>
              )
              return (
                <li key={`${mention.verbatimName}-${index}`}>
                  {findable ? (
                    <button
                      type="button"
                      className="flex min-h-9 w-full items-center gap-2 rounded-md px-1.5 text-start text-sm hover:bg-muted"
                      onClick={() =>
                        onReveal(
                          { kind: "mention", index },
                          `mention-${index}`
                        )
                      }
                    >
                      {body}
                    </button>
                  ) : (
                    <div className="flex min-h-9 w-full items-center gap-2 px-1.5 text-sm">
                      {body}
                    </div>
                  )}
                </li>
              )
            })}
          </ol>
        )}
      </RailGroup>

      <RailGroup
        title={`Sources${analysis.citations.length > 0 ? ` (${analysis.citations.length})` : ""}`}
      >
        {analysis.citations.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No sources were cited.
          </p>
        ) : (
          <ol className="flex flex-col gap-1">
            {analysis.citations.map((citation) => (
              <li
                key={`${citation.citeOrder}-${citation.url}`}
                id={`citation-${citation.citeOrder}`}
                className="scroll-mt-24 rounded-md px-1.5 py-1.5 hover:bg-muted"
              >
                <div className="flex items-start gap-2">
                  <button
                    type="button"
                    aria-label={`Find source ${citation.citeOrder + 1} in the answer`}
                    className="mt-0.5 shrink-0 rounded-sm bg-secondary px-1.5 text-xs font-medium text-secondary-foreground ring-1 ring-border hover:bg-background"
                    onClick={() =>
                      onReveal(
                        { kind: "citation", citeOrder: citation.citeOrder },
                        `citation-ref-${citation.citeOrder}`
                      )
                    }
                  >
                    {citation.citeOrder + 1}
                  </button>
                  <div className="flex min-w-0 flex-col">
                    <a
                      href={citation.url}
                      target="_blank"
                      rel="noreferrer"
                      className="inline-flex min-w-0 items-center gap-1 text-sm underline-offset-2 hover:underline"
                    >
                      <span className="truncate">
                        {citation.title || citation.domain}
                      </span>
                      <ExternalLink className="size-3 shrink-0" />
                    </a>
                    <span className="truncate text-xs text-muted-foreground">
                      {citation.domain} ·{" "}
                      {citationSubjectLabel(citation.subject)}
                    </span>
                  </div>
                </div>
              </li>
            ))}
          </ol>
        )}
      </RailGroup>

      {analysis.keywords.length > 0 && (
        // Themes are extracted phrases, not tags — they reach 60+ characters,
        // and a Badge is a fixed-height, non-wrapping pill. Rendered as rows
        // they wrap instead of forcing the rail into a horizontal scroll, and
        // they match how the Brief already lists keywords.
        <RailGroup title="Themes">
          <ul className="flex flex-col gap-1.5">
            {analysis.keywords.map((keyword) => (
              <li
                key={keyword}
                className="border-s-2 border-border ps-2.5 text-xs leading-5 text-muted-foreground"
              >
                {keyword}
              </li>
            ))}
          </ul>
        </RailGroup>
      )}
    </>
  )
}

function RailGroup({
  title,
  children,
}: {
  title: string
  children: React.ReactNode
}) {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="font-heading text-xs font-medium tracking-wide text-muted-foreground uppercase">
        {title}
      </h2>
      {children}
    </section>
  )
}

function TechnicalDetails({
  result,
  includeRaw,
  onToggleRaw,
  fetchingRaw,
}: {
  result: PromptResult
  includeRaw: boolean
  onToggleRaw: () => void
  fetchingRaw: boolean
}) {
  const path = useAccountPath()
  return (
    <details className="rounded-lg border">
      <summary className="cursor-pointer px-4 py-3 text-sm font-medium select-none">
        Technical details
      </summary>
      <div className="flex flex-col gap-5 border-t px-4 py-4">
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
        <section className="flex flex-col gap-2">
          <h3 className="font-heading text-sm font-medium">Request</h3>
          <JSONBlock
            json={result.requestJson}
            empty="No request params stored."
          />
        </section>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="w-fit"
          onClick={onToggleRaw}
        >
          {includeRaw ? "Hide raw JSON" : "Show raw JSON"}
        </Button>
        {includeRaw && (
          <JSONBlock
            json={result.rawResponseJson}
            empty={fetchingRaw ? "Loading raw JSON." : "No raw response stored."}
          />
        )}
      </div>
    </details>
  )
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

interface Replacement {
  replacedId: string
  replacedText: string
  on: Timestamp | undefined
}

// Walk the newest-first chain [prompt, ...lineage]: a node with a
// replaces_prompt_id replaced the next node in the chain, on that node's
// created_at. The API only exposes the backward chain (replaces_prompt_id), so
// this renders "replaced X" going back — there is no forward "replaced by Y" link.
function buildReplacements(prompt: Prompt, lineage: Prompt[]): Replacement[] {
  const chain = [prompt, ...lineage]
  const out: Replacement[] = []
  for (let i = 0; i < chain.length; i++) {
    const node = chain[i]
    const predecessor = chain[i + 1]
    if (node.replacesPromptId && predecessor) {
      out.push({
        replacedId: predecessor.id,
        replacedText: predecessor.text,
        on: node.createdAt,
      })
    }
  }
  return out
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

// mention_order is 0-based (order 0 = mentioned first), so render it as a
// 1-based English ordinal — "1st" must not read as the absent "0" value.
function ordinal(zeroBased: number): string {
  const n = zeroBased + 1
  const mod100 = n % 100
  if (mod100 >= 11 && mod100 <= 13) return `${n}th`
  switch (n % 10) {
    case 1:
      return `${n}st`
    case 2:
      return `${n}nd`
    case 3:
      return `${n}rd`
    default:
      return `${n}th`
  }
}

function DetailSkeleton() {
  return (
    <div className="flex flex-col gap-6">
      <Skeleton className="h-7 w-40" />
      <Skeleton className="h-8 w-2/3" />
      <Skeleton className="h-11 w-72" />
      <AnswerSkeleton />
    </div>
  )
}

function AnswerSkeleton() {
  return (
    <div className="grid items-start gap-8 lg:grid-cols-[minmax(0,1fr)_20rem]">
      <div className="flex flex-col gap-3">
        {Array.from({ length: 8 }, (_, i) => (
          <Skeleton key={i} className="h-4 w-full" />
        ))}
      </div>
      <Skeleton className="h-64 w-full" />
    </div>
  )
}

function formatDate(value: Timestamp | undefined): string {
  if (value === undefined) return "-"
  const date = timestampDate(value)
  if (Number.isNaN(date.valueOf())) return "-"
  return formatDateOnly(date)
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
