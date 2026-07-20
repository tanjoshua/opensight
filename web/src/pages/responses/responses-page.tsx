// Responses section (WEB-3, design 06): filterable, paginated list of stored
// ChatGPT responses. Filters live in the URL search params so trend charts can
// deep-link into a run (design 06 "every number is a door"); WEB-4 attaches the
// response drawer to these rows.
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

import { useMe } from "@/api/auth"
import {
  useResults,
  useRuns,
  type PromptResult,
  type ResultStatus,
  type Run,
} from "@/api/responses"
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
  const rawStatus = searchParams.get("status")
  const status: ResultStatus | undefined =
    rawStatus === "succeeded" || rawStatus === "failed" ? rawStatus : undefined
  const offset = Math.max(0, Number(searchParams.get("offset")) || 0)

  const runsQuery = useRuns(business?.id)
  const hasRunningRun =
    runsQuery.data?.runs.some((item) => item.status === "running") ?? false
  const resultsQuery = useResults(
    business?.id,
    {
      run,
      prompt,
      status,
      limit: PAGE_SIZE,
      offset,
    },
    { poll: hasRunningRun }
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
  const runningRuns = runs.filter((item) => item.status === "running")
  // The prompt filter is applied from a row (there is no prompts endpoint yet),
  // so label the chip from any loaded row of that prompt.
  const promptFilterText =
    prompt === undefined
      ? undefined
      : (results.find((r) => r.prompt_id === prompt)?.prompt?.text ??
        "1 prompt")
  const page = Math.floor(offset / PAGE_SIZE) + 1
  const hasNextPage = (resultsQuery.data?.paging.page_count ?? 0) === PAGE_SIZE

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
                  run={runsById.get(result.run_id)}
                  onOpen={() => setSelectedResultID(result.id)}
                  onFilterByPrompt={() => setFilter("prompt", result.prompt_id)}
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
          {result.prompt?.text ?? result.prompt_id}
        </span>
      </TableCell>
      <TableCell className="max-w-0">
        {result.status === "failed" ? (
          <span className="line-clamp-2 whitespace-normal text-destructive">
            {result.error ?? "Unknown error"}
          </span>
        ) : (
          <span className="line-clamp-2 whitespace-normal text-muted-foreground">
            {result.response_text}
          </span>
        )}
      </TableCell>
      <TableCell>
        <ResultStatusBadge status={result.status} />
      </TableCell>
      <TableCell>
        {run ? (
          <span className="flex items-center gap-1.5 whitespace-nowrap">
            {formatRunDate(run.scheduled_for)}
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
      label: `${formatRunDate(run.scheduled_for)} - ${run.status}`,
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
  value: ResultStatus | undefined
  onChange: (value?: string) => void
}) {
  const items = [
    { label: "All statuses", value: ALL_FILTER_VALUE },
    { label: "Succeeded", value: "succeeded" },
    { label: "Failed", value: "failed" },
  ]
  return (
    <Select
      items={items}
      value={value ?? ALL_FILTER_VALUE}
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

function ResultStatusBadge({ status }: { status: string }) {
  return (
    <Badge variant={status === "failed" ? "destructive" : "secondary"}>
      {status}
    </Badge>
  )
}

// Run-level status: completed / partial / failed (running until finished).
function RunStatusBadge({ status }: { status: string }) {
  const variant =
    status === "failed"
      ? ("destructive" as const)
      : status === "completed"
        ? ("secondary" as const)
        : ("outline" as const)
  return <Badge variant={variant}>{status}</Badge>
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
