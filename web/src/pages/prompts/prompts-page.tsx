// Prompts section (INS-2, design 06): the active-prompt table — mentioned?,
// mention order, sentiment, and a spark-trend across runs. Every stat opens the
// Response drawer via its result_id (every number is a door); a row click drills
// into the prompt's detail and lineage.
import { MessageSquareText, Plus } from "lucide-react"
import { useState } from "react"
import { useNavigate } from "react-router"

import { useMe } from "@/api/auth"
import { ApiError } from "@/api/client"
import {
  useAddPrompt,
  usePrompts,
  type PromptSummary,
  type PromptTrendPoint,
} from "@/api/prompts"
import { PromptConfirmDialog } from "@/components/prompt-confirm-dialog"
import { ResponseDrawer } from "@/components/response-drawer"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
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
  const me = useMe()
  const business = me.data?.businesses[0]
  const navigate = useNavigate()
  const promptsQuery = usePrompts(business?.id)
  const [selectedResultID, setSelectedResultID] = useState<string>()
  const [addOpen, setAddOpen] = useState(false)
  const addPrompt = useAddPrompt(business?.id)

  if (me.isError || promptsQuery.isError) {
    return (
      <SectionMessage
        title="Something went wrong"
        description="The prompts could not be loaded. Try reloading the page."
      />
    )
  }
  if (!me.data) {
    return <ListSkeleton />
  }
  if (!business) {
    return (
      <SectionMessage
        title="No business yet"
        description="Finish onboarding to start monitoring and collecting responses."
      />
    )
  }
  if (!promptsQuery.data) {
    return <ListSkeleton />
  }

  const prompts = promptsQuery.data.prompts

  const addError =
    addPrompt.error instanceof ApiError
      ? addPrompt.error.message
      : addPrompt.isError
        ? "Could not add prompt. Try again."
        : undefined

  const openAdd = (open: boolean) => {
    setAddOpen(open)
    if (!open) addPrompt.reset()
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-2">
        <h1 className="font-heading text-lg font-semibold">Prompts</h1>
        <Button onClick={() => openAdd(true)}>
          <Plus data-icon="inline-start" />
          Add prompt
        </Button>
      </div>

      <PromptConfirmDialog
        mode="add"
        open={addOpen}
        onOpenChange={openAdd}
        submitting={addPrompt.isPending}
        errorMessage={addError}
        onSubmit={(text) =>
          addPrompt.mutate(text, { onSuccess: () => setAddOpen(false) })
        }
      />

      <div className="overflow-x-auto rounded-lg border">
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
                  onOpenResult={setSelectedResultID}
                />
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <ResponseDrawer
        resultId={selectedResultID}
        onOpenChange={(open) => {
          if (!open) setSelectedResultID(undefined)
        }}
      />
    </div>
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
  // latest_result_id null means "not yet measured" — no analyzed result exists,
  // which is distinct from "measured, not mentioned" and has no door to open.
  const measured = prompt.latest_result_id !== null
  const openLatest = () => {
    if (prompt.latest_result_id) onOpenResult(prompt.latest_result_id)
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
        {prompt.order === null ? (
          <span className="text-muted-foreground">—</span>
        ) : (
          <StatButton onClick={openLatest} label="Open the latest response">
            {ordinal(prompt.order)}
          </StatButton>
        )}
      </TableCell>
      <TableCell>
        {prompt.sentiment === null ? (
          <span className="text-muted-foreground">—</span>
        ) : (
          <StatButton onClick={openLatest} label="Open the latest response">
            <span className="capitalize">{prompt.sentiment}</span>
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
}: {
  trend: PromptTrendPoint[]
  onOpenResult: (resultID: string) => void
}) {
  if (trend.length === 0) {
    return <span className="text-sm text-muted-foreground">No runs yet</span>
  }
  return (
    <div className="flex items-center gap-1.5">
      {trend.map((point) => (
        <button
          key={point.run_id}
          type="button"
          title={`${point.scheduled_for}: ${
            point.mentioned ? "mentioned" : "not mentioned"
          }`}
          aria-label={`${point.scheduled_for}: ${
            point.mentioned ? "mentioned" : "not mentioned"
          }`}
          className="cursor-pointer"
          onClick={(event) => {
            event.stopPropagation()
            onOpenResult(point.result_id)
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
