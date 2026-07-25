// Responses section (WEB-3, design 06): filterable, paginated list of stored
// ChatGPT responses. Filters live in the URL search params so trend charts can
// deep-link into a run (design 06 "every number is a door"); WEB-4 attaches the
// response drawer to these rows.
import { skipToken, useQuery } from "@connectrpc/connect-query"
import { keepPreviousData } from "@tanstack/react-query"
import {
  ChevronLeft,
  ChevronRight,
  ListFilter,
  MessagesSquare,
  PanelRightOpen,
  X,
} from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { useSearchParams } from "react-router"

import { resultStatusLabel, runStatusLabel } from "@/api/labels"
import { useMe } from "@/api/hooks"
import { ResultStatus, RunStatus } from "@/gen/opensight/v1/common_pb"
import type { PromptResult, Run } from "@/gen/opensight/v1/result_pb"
import {
  listResults,
  listRuns,
} from "@/gen/opensight/v1/result-ResultService_connectquery"
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

export function ResponsesPage() {
  const me = useMe()
  const business = me.data?.businesses[0]
  const [searchParams, setSearchParams] = useSearchParams()
  const [selectedResultID, setSelectedResultID] = useState<string>()

  const run = searchParams.get("run") ?? undefined
  const prompt = searchParams.get("prompt") ?? undefined
  const status = resultStatusFromParam(searchParams.get("status"))
  const offset = Math.max(0, Number(searchParams.get("offset")) || 0)

  // Re-created here and in app-layout.tsx's RunProgressBadge: the old REST
  // client centralized this poll-while-running behavior in one useRuns hook.
  const runsQuery = useQuery(
    listRuns,
    business === undefined ? skipToken : { businessId: business.id },
    {
      refetchInterval: (query) =>
        query.state.data?.runs.some((item) => item.status === RunStatus.RUNNING)
          ? 5000
          : false,
    }
  )
  const hasRunningRun =
    runsQuery.data?.runs.some((item) => item.status === RunStatus.RUNNING) ??
    false
  const resultsQuery = useQuery(
    listResults,
    business === undefined
      ? skipToken
      : {
          businessId: business.id,
          runId: run ?? "",
          promptId: prompt ?? "",
          status,
          limit: PAGE_SIZE,
          offset,
        },
    {
      placeholderData: keepPreviousData,
      refetchInterval: hasRunningRun ? 5000 : false,
    }
  )
  const refetchResults = resultsQuery.refetch
  const hadRunningRun = useRef(false)

  useEffect(() => {
    if (hadRunningRun.current && !hasRunningRun) {
      void refetchResults()
    }
    hadRunningRun.current = hasRunningRun
  }, [hasRunningRun, refetchResults])

  // Changing a filter resets paging: page N of the old filter is meaningless.
  const setFilter = (key: "run" | "prompt" | "status", value?: string) => {
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

  if (me.isError) {
    return (
      <SectionMessage
        title="Something went wrong"
        description="The responses could not be loaded. Try reloading the page."
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
  if (runsQuery.isError || resultsQuery.isError) {
    return (
      <SectionMessage
        title="Something went wrong"
        description="The responses could not be loaded. Try reloading the page."
      />
    )
  }
  if (!runsQuery.data) {
    return <ListSkeleton />
  }

  const runs = runsQuery.data.runs
  if (runs.length === 0) {
    return (
      <SectionMessage
        title="No runs yet"
        description="Responses appear here after your first weekly monitoring run. Check back once it has completed."
      />
    )
  }

  const results = resultsQuery.data?.results ?? []
  const runsById = new Map(runs.map((r) => [r.id, r]))
  const runningRuns = runs.filter((item) => item.status === RunStatus.RUNNING)
  // The prompt filter is applied from a row (there is no prompts endpoint yet),
  // so label the chip from any loaded row of that prompt.
  const promptFilterText =
    prompt === undefined
      ? undefined
      : (results.find((r) => r.promptId === prompt)?.prompt?.text ??
        "1 prompt")
  const page = Math.floor(offset / PAGE_SIZE) + 1
  const hasNextPage = (resultsQuery.data?.paging?.pageCount ?? 0) === PAGE_SIZE

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="font-heading text-lg font-semibold">Responses</h1>
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
          <RunFilter
            runs={runs}
            value={run}
            onChange={(v) => setFilter("run", v)}
          />
          <StatusFilter
            value={status}
            onChange={(v) => setFilter("status", v)}
          />
        </div>
      </div>

      {runningRuns.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border bg-muted/40 px-3 py-2 text-sm">
          <Badge variant="outline">Running</Badge>
          <span className="text-muted-foreground">
            {runningRuns.length === 1
              ? "A monitoring run is in progress. This list will refresh automatically."
              : `${runningRuns.length} monitoring runs are in progress. This list will refresh automatically.`}
          </span>
        </div>
      )}

      <div className="overflow-x-auto rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[30%]">Prompt</TableHead>
              <TableHead>Response</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Run</TableHead>
              <TableHead className="w-20" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {results.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={5}
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
                  run={runsById.get(result.runId)}
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
  run,
  onOpen,
  onFilterByPrompt,
}: {
  result: PromptResult
  run: Run | undefined
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
      <TableCell>
        {run ? (
          <span className="flex items-center gap-1.5 whitespace-nowrap">
            {formatRunDate(run.scheduledFor)}
            <RunStatusBadge status={run.status} />
          </span>
        ) : (
          "-"
        )}
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

function RunFilter({
  runs,
  value,
  onChange,
}: {
  runs: Run[]
  value: string | undefined
  onChange: (value?: string) => void
}) {
  const items = [
    { label: "All runs", value: ALL_FILTER_VALUE },
    ...runs.map((run) => ({
      label: `${formatRunDate(run.scheduledFor)} - ${runStatusLabel(run.status)}`,
      value: run.id,
    })),
  ]
  return (
    <Select
      items={items}
      value={value ?? ALL_FILTER_VALUE}
      onValueChange={(v) => onChange(filterValueFromSelect(v))}
    >
      <SelectTrigger size="sm" aria-label="Filter by run">
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
// param through the same words resultStatusLabel renders, so the URL contract
// (succeeded|failed) is unaffected by the REST-to-RPC cutover. An unrecognized
// or absent param means "no filter", i.e. ResultStatus.UNSPECIFIED.
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

// Run-level status: completed / partial / failed (running until finished).
function RunStatusBadge({ status }: { status: RunStatus }) {
  const variant =
    status === RunStatus.FAILED
      ? ("destructive" as const)
      : status === RunStatus.COMPLETED
        ? ("secondary" as const)
        : ("outline" as const)
  return <Badge variant={variant}>{runStatusLabel(status)}</Badge>
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
          <MessagesSquare />
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
      <div className="flex items-center gap-2">
        <Skeleton className="h-7 w-32" />
        <div className="ms-auto flex gap-2">
          <Skeleton className="h-7 w-28" />
          <Skeleton className="h-7 w-28" />
        </div>
      </div>
      <div className="flex flex-col gap-2">
        {Array.from({ length: 6 }, (_, i) => (
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
