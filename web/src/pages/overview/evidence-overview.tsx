import { ArrowRight } from "lucide-react"
import { Link } from "react-router"

import { CompetitorStatus } from "@/gen/opensight/v1/common_pb"
import type {
  CompetitorSummary,
  DomainStat,
} from "@/gen/opensight/v1/overview_pb"
import type { PromptSummary } from "@/gen/opensight/v1/prompt_pb"
import { formatPercent, pluralize } from "@/lib/format"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Panel, PanelEmpty, QuestionRow, StatRow } from "./panels"
import type { Overview } from "./shared"

const BRIEF_LIST_LIMIT = 5

export function EvidenceOverview({
  overview,
  prompts,
  onOpenResult,
  onOpenDomain,
}: {
  overview: Overview
  prompts: PromptSummary[]
  onOpenResult: (ids: string[], context?: string) => void
  onOpenDomain: (domain: DomainStat) => void
}) {
  const absent = prompts.filter(
    (prompt) => prompt.latestResultId !== undefined && !prompt.mentioned
  )
  return (
    <section
      className="flex flex-col gap-3"
      aria-labelledby="evidence-overview-title"
    >
      <div>
        <h2 id="evidence-overview-title" className="text-xl font-semibold">
          Explore the evidence
        </h2>
        <p className="text-sm text-muted-foreground">
          Open any item to see the responses behind it.
        </p>
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        <Panel
          title="Questions to review"
          description={`${absent.length} latest ${pluralize(absent.length, "response")} ${absent.length === 1 ? "does" : "do"} not mention you`}
          footer={
            <Link
              to="/prompts"
              className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
            >
              View all questions
              <ArrowRight className="size-4" />
            </Link>
          }
        >
          {absent.length === 0 ? (
            <PanelEmpty>No measured questions are currently absent.</PanelEmpty>
          ) : (
            absent
              .slice(0, BRIEF_LIST_LIMIT)
              .map((prompt) => (
                <QuestionRow
                  key={prompt.id}
                  prompt={prompt}
                  onClick={() =>
                    onOpenResult(
                      [prompt.latestResultId!],
                      `Latest response where you are absent: “${prompt.text}”`
                    )
                  }
                />
              ))
          )}
        </Panel>
        <CompetitorsPanel overview={overview} onOpenResult={onOpenResult} />
        <DomainsPanel overview={overview} onOpenDomain={onOpenDomain} />
        <ThemesPanel overview={overview} onOpenResult={onOpenResult} />
      </div>
    </section>
  )
}

function ThemesPanel({
  overview,
  onOpenResult,
}: {
  overview: Overview
  onOpenResult: (ids: string[], context?: string) => void
}) {
  return (
    <Panel
      title="Common themes"
      description="Keywords recurring across analyzed responses"
    >
      {overview.topKeywords.length === 0 ? (
        <PanelEmpty>No keywords yet.</PanelEmpty>
      ) : (
        overview.topKeywords
          .slice(0, BRIEF_LIST_LIMIT)
          .map((keyword) => (
            <StatRow
              key={keyword.keyword}
              label={keyword.keyword}
              value={`${keyword.resultIds.length} ${pluralize(keyword.resultIds.length, "response")}`}
              disabled={keyword.resultIds.length === 0}
              onClick={() =>
                onOpenResult(
                  keyword.resultIds,
                  `Responses associated with “${keyword.keyword}”`
                )
              }
            />
          ))
      )}
    </Panel>
  )
}

function DomainsPanel({
  overview,
  onOpenDomain,
}: {
  overview: Overview
  onOpenDomain: (domain: DomainStat) => void
}) {
  return (
    <Panel
      title="Cited sources"
      description="Domains ranked by the responses citing them"
    >
      {overview.topCitedDomains.length === 0 ? (
        <PanelEmpty>No citations yet.</PanelEmpty>
      ) : (
        overview.topCitedDomains
          .slice(0, BRIEF_LIST_LIMIT)
          .map((domain) => (
            <StatRow
              key={domain.domain}
              label={domain.domain}
              value={`${domain.resultIds.length} ${pluralize(domain.resultIds.length, "response")}`}
              disabled={domain.resultIds.length === 0}
              onClick={() => onOpenDomain(domain)}
            />
          ))
      )}
    </Panel>
  )
}

function CompetitorsPanel({
  overview,
  onOpenResult,
}: {
  overview: Overview
  onOpenResult: (ids: string[], context?: string) => void
}) {
  // top_competitors is truncated to the top-3 discovered, so the backlog count
  // comes from the server's untruncated discovered_total.
  const discovered = overview.discoveredTotal
  return (
    <Panel
      title="Competitors appearing"
      description="Ranked by coverage across analyzed responses"
      footer={
        <Link
          to={
            discovered > 0 ? "/competitors?status=discovered" : "/competitors"
          }
          className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
        >
          {discovered > 0
            ? `${discovered} discovered to review`
            : "View all competitors"}
          <ArrowRight className="size-4" />
        </Link>
      }
    >
      {overview.topCompetitors.length === 0 ? (
        <PanelEmpty>No competitors yet.</PanelEmpty>
      ) : (
        overview.topCompetitors
          .slice(0, BRIEF_LIST_LIMIT)
          .map((competitor) => (
            <CompetitorRow
              key={competitor.id}
              competitor={competitor}
              onClick={() =>
                onOpenResult(
                  competitor.resultIds,
                  `Responses mentioning ${competitor.name}`
                )
              }
            />
          ))
      )}
    </Panel>
  )
}

function CompetitorRow({
  competitor,
  onClick,
}: {
  competitor: CompetitorSummary
  onClick: () => void
}) {
  const disabled = competitor.resultIds.length === 0
  return (
    <Button
      type="button"
      variant="ghost"
      disabled={disabled}
      onClick={onClick}
      className="h-auto min-h-11 w-full justify-between gap-2 px-2 py-2 text-left whitespace-normal"
    >
      <span className="flex min-w-0 items-center gap-2">
        <span className="truncate">{competitor.name}</span>
        {competitor.status === CompetitorStatus.DISCOVERED && (
          <Badge variant="outline">discovered</Badge>
        )}
      </span>
      <span className="shrink-0 text-muted-foreground tabular-nums">
        {formatPercent(competitor.mentionPercent)}
      </span>
    </Button>
  )
}
