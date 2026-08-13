// Overview section (INS-1, design 06): one page answering "how visible am I and
// what changed". Headline visibility stat + weekly trend line with prompt-set
// change markers, then three compact panels (themes, cited domains, competitors).
// Every number is a door — stats open the Response drawer via their result_ids.
// The adaptive visibility explorer inspects historical runs without changing
// the independently scoped evidence elsewhere in the Brief.
import { skipToken, useQuery } from "@connectrpc/connect-query"
import { LayoutDashboard, TriangleAlert } from "lucide-react"
import { useState } from "react"
import { Link } from "react-router"
import { useAccountNavigate, useAccountPath } from "@/lib/account-path"

import { pollWhileRunning, useCurrentBusiness, usePlan } from "@/api/hooks"
import { RunStatus } from "@/gen/opensight/v1/common_pb"
import { getOverview } from "@/gen/opensight/v1/overview-OverviewService_connectquery"
import { listPrompts } from "@/gen/opensight/v1/prompt-PromptService_connectquery"
import { CitationSourcesDrilldown } from "@/components/citation-sources-drilldown"
import { PageHeader } from "@/components/page-header"
import { ResponseDrawer } from "@/components/response-drawer"
import { SectionMessage } from "@/components/section-message"
import {
  evidenceSelection,
  type EvidenceSelection,
} from "@/components/evidence-selection"
import { RunStageStrip } from "@/components/run-stage-strip"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { EvidenceOverview } from "./evidence-overview"
import type { ExplorerMode, Overview } from "./shared"
import { VisibilityCard } from "./visibility-card"
import { WhatChanged } from "./what-changed"

export function OverviewPage() {
  const { business, isError, isReady } = useCurrentBusiness()
  const { plan } = usePlan()
  const navigate = useAccountNavigate()
  // The API's readiness flag covers both the pre-run queue gap and response
  // analysis, then turns off before the independent Improve assessment ends.
  const overview = useQuery(
    getOverview,
    business === undefined ? skipToken : { businessId: business.id },
    {
      refetchInterval: pollWhileRunning(
        (data: Overview) => data.visibilityPending
      ),
    }
  )
  const prompts = useQuery(
    listPrompts,
    business === undefined ? skipToken : { businessId: business.id }
  )
  // Opening the drawer is the shared "every number is a door" action: retain
  // the complete ordered evidence set behind a metric.
  const [selectedEvidence, setSelectedEvidence] = useState<EvidenceSelection>()
  const [selectedCitationDomain, setSelectedCitationDomain] = useState<string>()
  const [explorerModeOverride, setExplorerModeOverride] =
    useState<ExplorerMode>()
  const openResult = (ids: string[], context?: string) =>
    setSelectedEvidence(
      evidenceSelection(ids, context ?? "Responses behind this overview metric")
    )

  if (isError) {
    return (
      <SectionMessage
        icon={LayoutDashboard}
        title="Something went wrong"
        description="The overview could not be loaded. Try reloading the page."
      />
    )
  }
  if (!isReady) {
    return <OverviewSkeleton />
  }
  if (!business) {
    return <OverviewSkeleton />
  }
  if (overview.isError || prompts.isError) {
    return (
      <OverviewFrame>
        <SectionMessage
          icon={LayoutDashboard}
          title="Something went wrong"
          description="The overview could not be loaded. Try reloading the page."
        />
      </OverviewFrame>
    )
  }
  if (!overview.data || !prompts.data) {
    return (
      <OverviewFrame>
        <OverviewSkeleton />
      </OverviewFrame>
    )
  }

  const data = overview.data
  const promptSummaries = prompts.data.prompts
  const trendLength = data.visibility?.trend.length ?? 0
  const explorerMode: ExplorerMode =
    trendLength < 2 ? "snapshot" : (explorerModeOverride ?? "trend")

  // No analyzed history yet: either no run has happened, the first run is still
  // in flight, or results are awaiting analysis (design 06 degraded states).
  if (trendLength === 0) {
    return (
      <OverviewFrame>
        <NoDataState overview={data} />
      </OverviewFrame>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <OverviewHeader />

      <PartialRunBanner overview={data} />

      <VisibilityCard
        overview={data}
        mode={explorerMode}
        runInterval={plan?.runInterval ?? ""}
        onModeChange={setExplorerModeOverride}
        onOpenResult={openResult}
        onSelectRun={(runID) => navigate(`/runs/${runID}`)}
      />

      {explorerMode === "trend" && (
        <WhatChanged
          prompts={promptSummaries}
          onOpenResult={(id, context) => openResult([id], context)}
        />
      )}

      <EvidenceOverview
        overview={data}
        prompts={promptSummaries}
        onOpenResult={openResult}
        onOpenDomain={(domain) => setSelectedCitationDomain(domain.domain)}
      />

      <CitationSourcesDrilldown
        businessId={business.id}
        domain={selectedCitationDomain}
        open={selectedCitationDomain !== undefined}
        onOpenChange={(open) => {
          if (!open) setSelectedCitationDomain(undefined)
        }}
        onOpenResult={openResult}
      />
      <ResponseDrawer
        evidence={selectedEvidence}
        onOpenChange={(open) => {
          if (!open) setSelectedEvidence(undefined)
        }}
      />
    </div>
  )
}

function OverviewFrame({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-4">
      <OverviewHeader />
      {children}
    </div>
  )
}

function OverviewHeader() {
  const path = useAccountPath()
  return (
    <PageHeader
      title="Your visibility brief"
      description="Your latest AI visibility signals and what changed."
      actions={
        <Link
          className="text-sm text-muted-foreground underline underline-offset-4 hover:text-foreground"
          to={path("/methodology")}
        >
          How we measure
        </Link>
      }
    />
  )
}

function PartialRunBanner({ overview }: { overview: Overview }) {
  const path = useAccountPath()
  const run = overview.latestRun
  // The overview payload carries run status but not per-prompt success counts, so
  // the banner names the condition and links to the failed responses rather than
  // inventing an "N of M" figure (design 06 partial-run state).
  if (
    !run ||
    (run.status !== RunStatus.PARTIAL && run.status !== RunStatus.FAILED)
  ) {
    return null
  }
  return (
    <Alert variant="destructive">
      <TriangleAlert />
      <AlertTitle>
        {run.status === RunStatus.FAILED
          ? "The latest run failed"
          : "Some prompts failed in the latest run"}
      </AlertTitle>
      <AlertDescription>
        <Link
          to={path(`/runs/${run.id}?status=failed`)}
          className="underline underline-offset-2"
        >
          Review the failed responses
        </Link>
      </AlertDescription>
    </Alert>
  )
}

// Trend is empty: no analyzed results exist yet. Distinguish "no run", "run in
// progress", and "awaiting analysis" so the honest state shows (design 06).
function NoDataState({ overview }: { overview: Overview }) {
  const run = overview.latestRun
  if (!run) {
    return (
      <SectionMessage
        icon={LayoutDashboard}
        title="No runs yet"
        description="Your visibility appears here after your first weekly monitoring run completes."
      />
    )
  }
  // One view for the whole pipeline. FinalizeRun sets the run's terminal status
  // before the analysis job finishes (see internal/jobs), so the run stops
  // being RUNNING while analysis is still in flight — branching on status here
  // would swap the user to a different component mid-pipeline. The stage strip
  // already renders that state as an active Analyzing step, so keep it mounted
  // and let the stages advance in place.
  return (
    <Empty className="border">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <LayoutDashboard />
        </EmptyMedia>
        <EmptyTitle>First run in progress</EmptyTitle>
        <EmptyDescription>
          <div className="w-full max-w-xs pt-2 text-left">
            <RunStageStrip run={run} variant="full" />
          </div>
        </EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}

function OverviewSkeleton() {
  return (
    <div className="flex flex-col gap-4">
      <Skeleton className="h-7 w-32" />
      <Skeleton className="h-64 w-full" />
      <div className="grid gap-4 md:grid-cols-3">
        <Skeleton className="h-48 w-full" />
        <Skeleton className="h-48 w-full" />
        <Skeleton className="h-48 w-full" />
      </div>
    </div>
  )
}
