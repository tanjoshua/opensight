// Runs section (RUNS-4, design 06, replaces Responses): a run-centric list —
// the weekly monitoring cadence, not a flat cross-run response list (that view
// is dropped; per-prompt history already lives on the Prompts page). Each row
// links to /runs/:id for that run's responses.
import { timestampDate } from "@bufbuild/protobuf/wkt"
import { History, LoaderCircle } from "lucide-react"
import { useAccountNavigate } from "@/lib/account-path"

import { useCurrentBusiness, useRuns } from "@/api/hooks"
import { runStatusLabel, runTriggerLabel } from "@/api/labels"
import { RunStatus } from "@/gen/opensight/v1/common_pb"
import type { Run } from "@/gen/opensight/v1/result_pb"
import { ListSkeleton } from "@/components/list-skeleton"
import { RunStageStrip } from "@/components/run-stage-strip"
import { PageHeader } from "@/components/page-header"
import { SectionMessage } from "@/components/section-message"
import { formatDateOnly, formatPercent, formatRunDate } from "@/lib/format"
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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

export function RunsPage() {
  const { business, isError, isReady } = useCurrentBusiness()
  const navigate = useAccountNavigate()
  const runsQuery = useRuns(business?.id)

  if (isError) {
    return (
      <SectionMessage
        icon={History}
        title="Something went wrong"
        description="The runs could not be loaded. Try reloading the page."
      />
    )
  }
  if (!isReady) {
    return <ListSkeleton />
  }
  if (!business) {
    return <ListSkeleton />
  }
  if (runsQuery.isError) {
    return (
      <SectionMessage
        icon={History}
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
      <PageHeader
        title="Monitoring history"
        description="Inspect each monitoring run and the responses behind your metrics."
        actions={
          nextRunAt ? (
            <span className="text-sm text-muted-foreground">
              Next run: {formatDateOnly(timestampDate(nextRunAt))}
            </span>
          ) : undefined
        }
      />

      {runs.length === 0 ? (
        runsQuery.data.monitoringPending ? (
          <SectionMessage
            icon={LoaderCircle}
            title="Preparing your first run"
            description="Your monitoring check is queued. This page will update automatically when it starts."
          />
        ) : (
          <SectionMessage
            icon={History}
            title="No runs yet"
            description={
              nextRunAt
                ? `Your first scheduled check is ${formatDateOnly(timestampDate(nextRunAt))}.`
                : "Monitoring starts once your business and plan are active."
            }
          />
        )
      ) : (
        <>
          <div className="flex flex-col gap-3 md:hidden">
            {runs.map((run) => (
              <RunMobileCard
                key={run.id}
                run={run}
                onNavigate={() => navigate(`/runs/${run.id}`)}
              />
            ))}
          </div>
          <div className="hidden rounded-lg border md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Date</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Responses</TableHead>
                  <TableHead>Visibility</TableHead>
                  <TableHead className="w-32 text-right">
                    <span className="sr-only">Actions</span>
                  </TableHead>
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
        </>
      )}
    </div>
  )
}

function RunMobileCard({
  run,
  onNavigate,
}: {
  run: Run
  onNavigate: () => void
}) {
  const trigger = runTriggerLabel(run.trigger)
  const expected =
    run.expectedResults ?? run.succeededResults + run.failedResults
  const unanalyzed =
    run.status !== RunStatus.RUNNING && run.analysisCompletedAt === undefined

  return (
    <Card size="sm" className="min-w-0">
      <CardHeader>
        <CardTitle className="flex flex-wrap items-center gap-2">
          {formatRunDate(run.scheduledFor)}
          {trigger !== undefined && <Badge variant="outline">{trigger}</Badge>}
        </CardTitle>
        <div className="flex flex-wrap items-center gap-1.5">
          <RunStatusBadge status={run.status} />
          {unanalyzed && <Badge variant="outline">not yet analyzed</Badge>}
        </div>
      </CardHeader>
      <CardContent>
        {run.status === RunStatus.RUNNING ? (
          <RunStageStrip run={run} variant="compact" />
        ) : (
          <dl className="grid grid-cols-2 gap-3 text-sm">
            <div className="flex flex-col gap-1">
              <dt className="text-xs text-muted-foreground">Responses</dt>
              <dd className="tabular-nums">
                {run.succeededResults} of {expected}
              </dd>
            </div>
            <div className="flex flex-col gap-1">
              <dt className="text-xs text-muted-foreground">Visibility</dt>
              <dd className="tabular-nums">
                {run.visibility === undefined
                  ? "—"
                  : formatPercent(run.visibility)}
              </dd>
            </div>
          </dl>
        )}
      </CardContent>
      <CardFooter>
        <Button
          type="button"
          variant="outline"
          className="min-h-11"
          onClick={onNavigate}
        >
          View run evidence
        </Button>
      </CardFooter>
    </Card>
  )
}

function RunRow({ run, onNavigate }: { run: Run; onNavigate: () => void }) {
  const trigger = runTriggerLabel(run.trigger)
  const expected =
    run.expectedResults ?? run.succeededResults + run.failedResults
  const unanalyzed =
    run.status !== RunStatus.RUNNING && run.analysisCompletedAt === undefined

  return (
    <TableRow>
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
      <TableCell className="text-muted-foreground tabular-nums">
        {run.visibility === undefined
          ? "—"
          : `${formatPercent(run.visibility)}`}
      </TableCell>
      <TableCell className="text-right">
        <Button type="button" variant="outline" size="sm" onClick={onNavigate}>
          View evidence
        </Button>
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
