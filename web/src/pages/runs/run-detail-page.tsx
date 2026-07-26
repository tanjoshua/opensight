// Run detail (RUNS-5, design 06): a single run's full stage progress plus its
// response table. Reached by drilling in from the Runs list or a run-scoped
// deep link (Overview's trend/partial-run links, the Competitors trend click).
// The run is read out of the already-polling useRuns cache — no GetRun.
import { skipToken, useQuery } from "@connectrpc/connect-query"
import { keepPreviousData } from "@tanstack/react-query"
import {
  ArrowLeft,
  ChevronLeft,
  ChevronRight,
  History,
  ListFilter,
  PanelRightOpen,
  X,
} from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { useNavigate, useParams, useSearchParams } from "react-router"

import { resultStatusLabel } from "@/api/labels"
import { RUN_POLL_INTERVAL_MS, useCurrentBusiness, useRuns } from "@/api/hooks"
import { ResultStatus, RunStatus } from "@/gen/opensight/v1/common_pb"
import type { PromptResult } from "@/gen/opensight/v1/result_pb"
import { listResults } from "@/gen/opensight/v1/result-ResultService_connectquery"
import { ResponseDrawer } from "@/components/response-drawer"
import { RunStageStrip } from "@/components/run-stage-strip"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

const PAGE_SIZE = 20
const ALL_FILTER_VALUE = "all"

export function RunDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { businessId } = useCurrentBusiness()
  const [searchParams, setSearchParams] = useSearchParams()
  const [selectedResultID, setSelectedResultID] = useState<string>()

  const prompt = searchParams.get("prompt") ?? undefined
  const status = resultStatusFromParam(searchParams.get("status"))
  const offset = Math.max(0, Number(searchParams.get("offset")) || 0)

  const runsQuery = useRuns(businessId)
  const run = runsQuery.data?.runs.find((r) => r.id === id)
  const isRunning = run?.status === RunStatus.RUNNING

  const resultsQuery = useQuery(
    listResults,
    businessId === undefined || id === undefined
      ? skipToken
      : {
          businessId,
          runId: id,
          promptId: prompt ?? "",
          status,
          limit: PAGE_SIZE,
          offset,
        },
    {
      placeholderData: keepPreviousData,
      refetchInterval: isRunning ? RUN_POLL_INTERVAL_MS : false,
    }
  )
  const refetchResults = resultsQuery.refetch
  const hadRunningRun = useRef(false)

  useEffect(() => {
    if (hadRunningRun.current && !isRunning) {
      void refetchResults()
    }
    hadRunningRun.current = isRunning
  }, [isRunning, refetchResults])

  const setFilter = (key: "prompt" | "status", value?: string) => {
    const next = new URLSearchParams(searchParams)
    if (value === undefined) next.delete(key)
    else next.set(key, value)
    next.delete("offset")
    setSearchParams(next)
  }
  const setOffset = (value: number) => {
    const next = new URLSearchParams(searchParams)
    if (value <= 0) next.delete("offset")
    else next.set("offset", String(value))
    setSearchParams(next)
  }

  if (runsQuery.isError) {
    return (
      <SectionMessage
        title="Something went wrong"
        description="The run could not be loaded. Try reloading the page."
      />
    )
  }
  if (!runsQuery.data) {
    return <DetailSkeleton />
  }
  if (!run) {
    return (
      <SectionMessage
        title="Run not found"
        description="This run could not be loaded. It may have been removed."
      />
    )
  }

  const results = resultsQuery.data?.results ?? []
  const promptFilterText =
    prompt === undefined
      ? undefined
      : (results.find((r) => r.promptId === prompt)?.prompt?.text ?? "1 prompt")
  const page = Math.floor(offset / PAGE_SIZE) + 1
  const hasNextPage = (resultsQuery.data?.paging?.pageCount ?? 0) === PAGE_SIZE

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-2">
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Back to runs"
          onClick={() => navigate("/runs")}
        >
          <ArrowLeft />
        </Button>
        <h1 className="font-heading text-lg font-semibold">
          Run — {formatRunDate(run.scheduledFor)}
        </h1>
      </div>

      <div className="rounded-lg border p-4">
        <RunStageStrip
          run={run}
          variant="full"
          onShowFailed={() => setFilter("status", resultStatusToParam(ResultStatus.FAILED))}
        />
        <details className="mt-3 text-xs text-muted-foreground">
          <summary className="cursor-pointer select-none">
            Technical details
          </summary>
          <div className="mt-1">Workflow ID: {run.workflowId}</div>
        </details>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <h2 className="font-heading text-sm font-medium">Responses</h2>
        <div className="ms-auto flex flex-wrap items-center gap-2">
          {promptFilterText !== undefined && (
            <Badge variant="secondary" className="max-w-56">
              <span className="truncate">Prompt: {promptFilterText}</span>
              <button
                type="button"
                aria-label="Clear prompt filter"
                className="cursor-pointer"
                onClick={() => setFilter("prompt")}
              >
                <X />
              </button>
            </Badge>
          )}
          <StatusFilter value={status} onChange={(v) => setFilter("status", v)} />
        </div>
      </div>

      <div className="overflow-x-auto rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[40%]">Prompt</TableHead>
              <TableHead>Response</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="w-20" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {results.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={4}
                  className="h-24 text-center text-muted-foreground"
                >
                  No responses match the current filters.
                </TableCell>
              </TableRow>
            ) : (
              results.map((result) => (
                <ResultRow
                  key={result.id}
                  result={result}
                  onOpen={() => setSelectedResultID(result.id)}
                  onFilterByPrompt={() => setFilter("prompt", result.promptId)}
                />
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <div className="flex items-center justify-end gap-2">
        <span className="text-sm text-muted-foreground">Page {page}</span>
        <Button
          variant="outline"
          size="icon-sm"
          aria-label="Previous page"
          disabled={offset === 0 || resultsQuery.isPlaceholderData}
          onClick={() => setOffset(offset - PAGE_SIZE)}
        >
          <ChevronLeft />
        </Button>
        <Button
          variant="outline"
          size="icon-sm"
          aria-label="Next page"
          disabled={!hasNextPage || resultsQuery.isPlaceholderData}
          onClick={() => setOffset(offset + PAGE_SIZE)}
        >
          <ChevronRight />
        </Button>
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

function ResultRow({
  result,
  onOpen,
  onFilterByPrompt,
}: {
  result: PromptResult
  onOpen: () => void
  onFilterByPrompt: () => void
}) {
  return (
    <TableRow className="cursor-pointer" onClick={onOpen}>
      <TableCell className="max-w-0">
        <span className="line-clamp-2 whitespace-normal">
          {result.prompt?.text ?? result.promptId}
        </span>
      </TableCell>
      <TableCell className="max-w-0">
        {result.status === ResultStatus.FAILED ? (
          <span className="line-clamp-2 whitespace-normal text-destructive">
            {result.error ?? "Unknown error"}
          </span>
        ) : (
          <span className="line-clamp-2 whitespace-normal text-muted-foreground">
            {result.responseText}
          </span>
        )}
      </TableCell>
      <TableCell>
        <span className="flex flex-wrap items-center gap-1.5">
          <ResultStatusBadge status={result.status} />
          {result.unanalyzed && (
            <Badge variant="outline">not yet analyzed</Badge>
          )}
        </span>
      </TableCell>
      <TableCell className="whitespace-nowrap">
        <Button
          variant="ghost"
          size="icon-xs"
          aria-label="Open response"
          title="Open response"
          onClick={(event) => {
            event.stopPropagation()
            onOpen()
          }}
        >
          <PanelRightOpen />
        </Button>
        <Button
          variant="ghost"
          size="icon-xs"
          aria-label="Filter by this prompt"
          title="Filter by this prompt"
          onClick={(event) => {
            event.stopPropagation()
            onFilterByPrompt()
          }}
        >
          <ListFilter />
        </Button>
      </TableCell>
    </TableRow>
  )
}

function StatusFilter({
  value,
  onChange,
}: {
  value: ResultStatus
  onChange: (value?: string) => void
}) {
  const items = [
    { label: "All statuses", value: ALL_FILTER_VALUE },
    { label: "Succeeded", value: resultStatusLabel(ResultStatus.SUCCEEDED) },
    { label: "Failed", value: resultStatusLabel(ResultStatus.FAILED) },
  ]
  return (
    <Select
      items={items}
      value={resultStatusToParam(value) ?? ALL_FILTER_VALUE}
      onValueChange={(v) => onChange(filterValueFromSelect(v))}
    >
      <SelectTrigger size="sm" aria-label="Filter by status">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          {items.map((item) => (
            <SelectItem key={item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  )
}

function filterValueFromSelect(value: string | null | undefined) {
  const selected = value ?? ALL_FILTER_VALUE
  return selected === ALL_FILTER_VALUE ? undefined : selected
}

// resultStatusFromParam/resultStatusToParam round-trip the URL's ?status=
// param through the same words resultStatusLabel renders (mirrors the former
// responses-page.tsx), so the partial-run banner's status=failed deep link
// keeps working. An unrecognized or absent param means "no filter".
function resultStatusFromParam(value: string | null): ResultStatus {
  if (value === resultStatusLabel(ResultStatus.SUCCEEDED)) {
    return ResultStatus.SUCCEEDED
  }
  if (value === resultStatusLabel(ResultStatus.FAILED)) {
    return ResultStatus.FAILED
  }
  return ResultStatus.UNSPECIFIED
}

function resultStatusToParam(status: ResultStatus): string | undefined {
  return status === ResultStatus.UNSPECIFIED
    ? undefined
    : resultStatusLabel(status)
}

function ResultStatusBadge({ status }: { status: ResultStatus }) {
  return (
    <Badge variant={status === ResultStatus.FAILED ? "destructive" : "secondary"}>
      {resultStatusLabel(status)}
    </Badge>
  )
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
          <History />
        </EmptyMedia>
        <EmptyTitle>{title}</EmptyTitle>
        <EmptyDescription>{description}</EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}

function DetailSkeleton() {
  return (
    <div className="flex flex-col gap-4">
      <Skeleton className="h-7 w-40" />
      <Skeleton className="h-32 w-full" />
      <div className="flex flex-col gap-2">
        {Array.from({ length: 4 }, (_, i) => (
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </div>
    </div>
  )
}

function formatRunDate(scheduledFor: string): string {
  // scheduled_for is a plain date (YYYY-MM-DD); parse as local, not UTC.
  const date = new Date(`${scheduledFor}T00:00:00`)
  return date.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  })
}
