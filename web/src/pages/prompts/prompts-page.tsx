// Prompts section (INS-2, design 06): the active-prompt table — mentioned?,
// mention order, sentiment, and a spark-trend across runs. Every stat opens the
// Response drawer via its result_id (every number is a door); a row click drills
// into the prompt's detail and lineage.
import {
  createConnectQueryKey,
  skipToken,
  useMutation,
  useQuery,
} from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { MessageSquareText, Plus } from "lucide-react"
import { useState } from "react"
import { useNavigate } from "react-router"

import { errorMessage } from "@/api/errors"
import { useCurrentBusiness } from "@/api/hooks"
import { sentimentLabel } from "@/api/labels"
import { Sentiment } from "@/gen/opensight/v1/common_pb"
import type {
  PromptSummary,
  PromptTrendPoint,
} from "@/gen/opensight/v1/prompt_pb"
import {
  addPrompt,
  listPrompts,
} from "@/gen/opensight/v1/prompt-PromptService_connectquery"
import { PromptConfirmDialog } from "@/components/prompt-confirm-dialog"
import { PageHeader } from "@/components/page-header"
import { ResponseDrawer } from "@/components/response-drawer"
import {
  evidenceSelection,
  type EvidenceSelection,
} from "@/components/evidence-selection"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

export function PromptsPage() {
  const { business, isError, isReady } = useCurrentBusiness()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const promptsQuery = useQuery(
    listPrompts,
    business === undefined ? skipToken : { businessId: business.id }
  )
  const [selectedEvidence, setSelectedEvidence] = useState<EvidenceSelection>()
  const [addOpen, setAddOpen] = useState(false)
  const addPromptMutation = useMutation(addPrompt, {
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: listPrompts,
          input:
            business === undefined ? undefined : { businessId: business.id },
          cardinality: "finite",
        }),
      })
    },
  })

  if (isError || promptsQuery.isError) {
    return (
      <SectionMessage
        title="Something went wrong"
        description="The prompts could not be loaded. Try reloading the page."
      />
    )
  }
  if (!isReady) {
    return <ListSkeleton />
  }
  if (!business) {
    return <ListSkeleton />
  }
  if (!promptsQuery.data) {
    return <ListSkeleton />
  }

  const prompts = promptsQuery.data.prompts

  const addError = addPromptMutation.isError
    ? errorMessage(addPromptMutation.error, "Could not add prompt. Try again.")
    : undefined

  const openAdd = (open: boolean) => {
    setAddOpen(open)
    if (!open) addPromptMutation.reset()
  }

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Questions"
        description="The customer questions OpenSight checks in every monitoring run."
        actions={
          <Button className="min-h-11 md:min-h-0" onClick={() => openAdd(true)}>
            <Plus data-icon="inline-start" />
            Add question
          </Button>
        }
      />

      <PromptConfirmDialog
        mode="add"
        open={addOpen}
        onOpenChange={openAdd}
        submitting={addPromptMutation.isPending}
        errorMessage={addError}
        onSubmit={(text) =>
          addPromptMutation.mutate(
            { businessId: business.id, text },
            { onSuccess: () => setAddOpen(false) }
          )
        }
      />

      {prompts.length === 0 ? (
        <div className="md:hidden">
          <SectionMessage
            title="No questions yet"
            description="Questions appear here once monitoring is set up."
          />
        </div>
      ) : (
        <div className="flex flex-col gap-3 md:hidden">
          {prompts.map((prompt) => (
            <PromptMobileCard
              key={prompt.id}
              prompt={prompt}
              onNavigate={() => navigate(`/prompts/${prompt.id}`)}
              onOpenResult={(resultId) =>
                setSelectedEvidence(
                  evidenceSelection(
                    [resultId],
                    "Latest response for this question"
                  )
                )
              }
            />
          ))}
        </div>
      )}

      <div className="hidden rounded-lg border md:block">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[40%]">Prompt</TableHead>
              <TableHead>Mentioned</TableHead>
              <TableHead>Order</TableHead>
              <TableHead>Sentiment</TableHead>
              <TableHead>Trend</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {prompts.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={5}
                  className="h-24 text-center text-muted-foreground"
                >
                  No prompts yet. They appear here once monitoring is set up.
                </TableCell>
              </TableRow>
            ) : (
              prompts.map((prompt) => (
                <PromptRow
                  key={prompt.id}
                  prompt={prompt}
                  onNavigate={() => navigate(`/prompts/${prompt.id}`)}
                  onOpenResult={(resultId) =>
                    setSelectedEvidence(
                      evidenceSelection(
                        [resultId],
                        "Latest response for this question"
                      )
                    )
                  }
                />
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <ResponseDrawer
        evidence={selectedEvidence}
        onOpenChange={(open) => {
          if (!open) setSelectedEvidence(undefined)
        }}
      />
    </div>
  )
}

function PromptMobileCard({
  prompt,
  onNavigate,
  onOpenResult,
}: {
  prompt: PromptSummary
  onNavigate: () => void
  onOpenResult: (resultID: string) => void
}) {
  const measured = prompt.latestResultId !== undefined
  return (
    <Card size="sm" className="min-w-0">
      <CardHeader>
        <CardTitle>
          <button
            type="button"
            className="min-h-11 text-left"
            onClick={onNavigate}
          >
            {prompt.text}
          </button>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <dl className="grid grid-cols-3 gap-3 text-sm">
          <div className="flex min-w-0 flex-col gap-1">
            <dt className="text-xs text-muted-foreground">Mentioned</dt>
            <dd>
              {!measured ? (
                <span className="text-muted-foreground">Not measured</span>
              ) : (
                <Badge variant={prompt.mentioned ? "secondary" : "outline"}>
                  {prompt.mentioned ? "Yes" : "No"}
                </Badge>
              )}
            </dd>
          </div>
          <div className="flex min-w-0 flex-col gap-1">
            <dt className="text-xs text-muted-foreground">Order</dt>
            <dd className="tabular-nums">
              {prompt.order === undefined ? "—" : ordinal(prompt.order)}
            </dd>
          </div>
          <div className="flex min-w-0 flex-col gap-1">
            <dt className="text-xs text-muted-foreground">Sentiment</dt>
            <dd className="truncate capitalize">
              {prompt.sentiment === Sentiment.UNSPECIFIED
                ? "—"
                : sentimentLabel(prompt.sentiment)}
            </dd>
          </div>
        </dl>
        {prompt.trend.length > 0 && (
          <div className="flex flex-col gap-1">
            <span className="text-xs text-muted-foreground">
              Response history
            </span>
            <Sparkline
              trend={prompt.trend}
              onOpenResult={onOpenResult}
              largeTargets
            />
          </div>
        )}
      </CardContent>
      <CardFooter className="flex-wrap gap-2">
        {measured && (
          <Button
            type="button"
            variant="outline"
            className="min-h-11"
            onClick={() => {
              if (prompt.latestResultId) onOpenResult(prompt.latestResultId)
            }}
          >
            View latest response
          </Button>
        )}
        <Button
          type="button"
          variant="ghost"
          className="min-h-11"
          onClick={onNavigate}
        >
          View question details
        </Button>
      </CardFooter>
    </Card>
  )
}

function PromptRow({
  prompt,
  onNavigate,
  onOpenResult,
}: {
  prompt: PromptSummary
  onNavigate: () => void
  onOpenResult: (resultID: string) => void
}) {
  // latest_result_id absent means "not yet measured" — no analyzed result
  // exists, which is distinct from "measured, not mentioned" and has no door
  // to open.
  const measured = prompt.latestResultId !== undefined
  const openLatest = () => {
    if (prompt.latestResultId) onOpenResult(prompt.latestResultId)
  }

  return (
    <TableRow className="cursor-pointer" onClick={onNavigate}>
      <TableCell className="max-w-0">
        <span className="line-clamp-2 whitespace-normal">{prompt.text}</span>
      </TableCell>
      <TableCell>
        {!measured ? (
          <span className="text-sm text-muted-foreground">Not measured</span>
        ) : (
          <StatButton onClick={openLatest} label="Open the latest response">
            <Badge variant={prompt.mentioned ? "secondary" : "outline"}>
              {prompt.mentioned ? "Yes" : "No"}
            </Badge>
          </StatButton>
        )}
      </TableCell>
      <TableCell className="tabular-nums">
        {prompt.order === undefined ? (
          <span className="text-muted-foreground">—</span>
        ) : (
          <StatButton onClick={openLatest} label="Open the latest response">
            {ordinal(prompt.order)}
          </StatButton>
        )}
      </TableCell>
      <TableCell>
        {prompt.sentiment === Sentiment.UNSPECIFIED ? (
          <span className="text-muted-foreground">—</span>
        ) : (
          <StatButton onClick={openLatest} label="Open the latest response">
            <span className="capitalize">
              {sentimentLabel(prompt.sentiment)}
            </span>
          </StatButton>
        )}
      </TableCell>
      <TableCell>
        <Sparkline trend={prompt.trend} onOpenResult={onOpenResult} />
      </TableCell>
    </TableRow>
  )
}

// A wrapper that makes a stat clickable without triggering the row's navigate.
function StatButton({
  onClick,
  label,
  children,
}: {
  onClick: () => void
  label: string
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      className="cursor-pointer rounded hover:opacity-70"
      onClick={(event) => {
        event.stopPropagation()
        onClick()
      }}
    >
      {children}
    </button>
  )
}

// The spark-trend is a small run of dots, one per run oldest-first: a filled dot
// where the business was mentioned, a hollow ring where it wasn't. Each dot is a
// door to that run's response. A single run renders as one labeled dot rather
// than a degenerate line, matching the Overview trend's single-point posture.
function Sparkline({
  trend,
  onOpenResult,
  largeTargets = false,
}: {
  trend: PromptTrendPoint[]
  onOpenResult: (resultID: string) => void
  largeTargets?: boolean
}) {
  if (trend.length === 0) {
    return <span className="text-sm text-muted-foreground">No runs yet</span>
  }
  return (
    <div className="flex items-center gap-1.5">
      {trend.map((point) => (
        <button
          key={point.runId}
          type="button"
          title={`${point.scheduledFor}: ${
            point.mentioned ? "mentioned" : "not mentioned"
          }`}
          aria-label={`${point.scheduledFor}: ${
            point.mentioned ? "mentioned" : "not mentioned"
          }`}
          className={
            largeTargets
              ? "flex size-11 cursor-pointer items-center justify-center"
              : "cursor-pointer"
          }
          onClick={(event) => {
            event.stopPropagation()
            onOpenResult(point.resultId)
          }}
        >
          <span
            className={
              point.mentioned
                ? "block size-2.5 rounded-full bg-primary"
                : "block size-2.5 rounded-full border border-muted-foreground/60"
            }
          />
        </button>
      ))}
    </div>
  )
}

// mention_order is a 0-based index (order 0 = mentioned first), so render it as
// a 1-based English ordinal — "1st" must not read as the absent "0"/"—" value.
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

function SectionMessage({
  title,
  description,
}: {
  title: string
  description: string
}) {
  return (
    <Empty className="border">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <MessageSquareText />
        </EmptyMedia>
        <EmptyTitle>{title}</EmptyTitle>
        <EmptyDescription>{description}</EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}

function ListSkeleton() {
  return (
    <div className="flex flex-col gap-4">
      <Skeleton className="h-7 w-32" />
      <div className="flex flex-col gap-2">
        {Array.from({ length: 6 }, (_, i) => (
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </div>
    </div>
  )
}
