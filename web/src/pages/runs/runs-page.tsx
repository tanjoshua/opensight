// Runs section (RUNS-4, design 06, replaces Responses): a run-centric list —
// the weekly monitoring cadence, not a flat cross-run response list (that view
// is dropped; per-prompt history already lives on the Prompts page). Each row
// links to /runs/:id for that run's responses.
import { timestampDate } from "@bufbuild/protobuf/wkt"
import { History } from "lucide-react"
import { useNavigate } from "react-router"

import { useCurrentBusiness, useRuns } from "@/api/hooks"
import { runStatusLabel, runTriggerLabel } from "@/api/labels"
import { RunStatus } from "@/gen/opensight/v1/common_pb"
import type { Run } from "@/gen/opensight/v1/result_pb"
import { RunStageStrip } from "@/components/run-stage-strip"
import { Badge } from "@/components/ui/badge"
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

export function RunsPage() {
  const { business, isError, isReady } = useCurrentBusiness()
  const navigate = useNavigate()
  const runsQuery = useRuns(business?.id)

  if (isError) {
    return (
      <SectionMessage
        title="Something went wrong"
        description="The runs could not be loaded. Try reloading the page."
      />
    )
  }
  if (!isReady) {
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
  if (runsQuery.isError) {
    return (
      <SectionMessage
        title="Something went wrong"
        description="The runs could not be loaded. Try reloading the page."
      />
    )
  }
  if (!runsQuery.data) {
    return <ListSkeleton />
  }

  const runs = runsQuery.data.runs
  const nextRunAt = runsQuery.data.nextRunAt

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="font-heading text-lg font-semibold">Runs</h1>
        {nextRunAt && (
          <span className="text-sm text-muted-foreground">
            Next run: {formatDateOnly(timestampDate(nextRunAt))}
          </span>
        )}
      </div>

      {runs.length === 0 ? (
        <SectionMessage
          title="No runs yet"
          description="Runs appear here after your first weekly monitoring run. Check back once it has started."
        />
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Date</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Responses</TableHead>
                <TableHead>Visibility</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {runs.map((run) => (
                <RunRow
                  key={run.id}
                  run={run}
                  onNavigate={() => navigate(`/runs/${run.id}`)}
                />
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  )
}

function RunRow({ run, onNavigate }: { run: Run; onNavigate: () => void }) {
  const trigger = runTriggerLabel(run.trigger)
  const expected = run.expectedResults ?? run.succeededResults + run.failedResults
  const unanalyzed = run.status !== RunStatus.RUNNING && run.analysisCompletedAt === undefined

  return (
    <TableRow className="cursor-pointer" onClick={onNavigate}>
      <TableCell className="whitespace-nowrap">
        <span className="flex items-center gap-1.5">
          {formatRunDate(run.scheduledFor)}
          {trigger !== undefined && <Badge variant="outline">{trigger}</Badge>}
        </span>
      </TableCell>
      <TableCell>
        <span className="flex flex-wrap items-center gap-1.5">
          <RunStatusBadge status={run.status} />
          {unanalyzed && <Badge variant="outline">not yet analyzed</Badge>}
        </span>
      </TableCell>
      <TableCell>
        {run.status === RunStatus.RUNNING ? (
          <RunStageStrip run={run} variant="compact" />
        ) : (
          <span className="tabular-nums">
            {run.succeededResults} of {expected} responses
          </span>
        )}
      </TableCell>
      <TableCell className="tabular-nums text-muted-foreground">
        {run.visibility === undefined ? "—" : `${formatPercent(run.visibility)}`}
      </TableCell>
    </TableRow>
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
          <History />
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

function formatPercent(value: number): string {
  return `${(Math.round(value * 10) / 10).toFixed(1)}%`
}

// scheduled_for is a plain date (YYYY-MM-DD); parse as local, not UTC.
function formatRunDate(scheduledFor: string): string {
  return formatDateOnly(new Date(`${scheduledFor}T00:00:00`))
}

function formatDateOnly(date: Date): string {
  return date.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  })
}
