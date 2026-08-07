import { timestampDate } from "@bufbuild/protobuf/wkt"
import {
  createConnectQueryKey,
  skipToken,
  useMutation,
  useQuery,
} from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import {
  Activity,
  CheckCircle2,
  ChevronRight,
  CircleDashed,
  ClipboardCheck,
  ExternalLink,
  Lightbulb,
} from "lucide-react"
import { Link, useParams, useSearchParams } from "react-router"
import { useState } from "react"

import { useCurrentBusiness } from "@/api/hooks"
import { ListSkeleton } from "@/components/list-skeleton"
import { PageHeader } from "@/components/page-header"
import { ResponseDrawer } from "@/components/response-drawer"
import {
  evidenceSelection,
  type EvidenceSelection,
} from "@/components/evidence-selection"
import { SectionMessage } from "@/components/section-message"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import type { ImprovementAction } from "@/gen/opensight/v1/improve_pb"
import {
  ActionsEmptyReason,
  ActionStatus,
  ChecklistStanding,
  DismissalReason,
} from "@/gen/opensight/v1/improve_pb"
import {
  getAction,
  getChecklist,
  listActions,
  listActivity,
  setActionStatus,
} from "@/gen/opensight/v1/improve-ImproveService_connectquery"
import { accountPath } from "@/lib/account-path"
import { filterChecklistSections } from "@/pages/improve/checklist-filter"

const standingLabels: Record<number, string> = {
  [ChecklistStanding.GOOD]: "Good",
  [ChecklistStanding.IMPROVABLE]: "Improvable",
  [ChecklistStanding.NEEDS_ATTENTION]: "Needs attention",
  [ChecklistStanding.TRACKING]: "Tracking",
  [ChecklistStanding.COULD_NOT_VERIFY]: "Could not verify",
  [ChecklistStanding.NOT_ASSESSED]: "Not assessed",
  [ChecklistStanding.NOT_APPLICABLE]: "Not applicable",
  [ChecklistStanding.NO_LONGER_TRACKED]: "No longer tracked",
}

const actionStatusLabels: Record<number, string> = {
  [ActionStatus.OPEN]: "Open",
  [ActionStatus.IN_PROGRESS]: "In progress",
  [ActionStatus.COMPLETED]: "Completed",
  [ActionStatus.DISMISSED]: "Dismissed",
  [ActionStatus.RETIRED]: "Retired",
  [ActionStatus.SUPERSEDED]: "Superseded",
}

function formatDate(value: Parameters<typeof timestampDate>[0] | undefined) {
  return value
    ? timestampDate(value).toLocaleString(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      })
    : "Not yet checked"
}

function FreshnessNotice({
  freshness,
}: {
  freshness?: { available: boolean; partial: boolean; stale: boolean }
}) {
  if (!freshness?.available)
    return (
      <p className="text-sm text-muted-foreground">
        OpenSight has not completed a visibility assessment yet.
      </p>
    )
  if (!freshness.partial && !freshness.stale) return null
  return (
    <Alert>
      <AlertDescription>
        Some checks could not finish. Last successful results remain visible and
        are marked stale.
      </AlertDescription>
    </Alert>
  )
}

function useActionMutation(businessId?: string) {
  const queryClient = useQueryClient()
  return useMutation(setActionStatus, {
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: createConnectQueryKey({
            schema: listActions,
            input: businessId ? { businessId } : undefined,
            cardinality: "finite",
          }),
        }),
        queryClient.invalidateQueries({
          queryKey: createConnectQueryKey({
            schema: getAction,
            cardinality: "finite",
          }),
        }),
        queryClient.invalidateQueries({
          queryKey: createConnectQueryKey({
            schema: getChecklist,
            input: businessId ? { businessId } : undefined,
            cardinality: "finite",
          }),
        }),
        queryClient.invalidateQueries({
          queryKey: createConnectQueryKey({
            schema: listActivity,
            cardinality: "finite",
          }),
        }),
      ])
    },
  })
}

export function ActionsPage() {
  const { business, isError, isReady } = useCurrentBusiness()
  const query = useQuery(
    listActions,
    business ? { businessId: business.id } : skipToken
  )
  const mutation = useActionMutation(business?.id)
  const [params] = useSearchParams()
  const selectedId = params.get("action") ?? ""
  const selected = useQuery(
    getAction,
    selectedId ? { actionId: selectedId } : skipToken
  )
  if (isError || query.isError || selected.isError)
    return (
      <SectionMessage
        icon={Lightbulb}
        title="Next actions could not be loaded"
        description="Try reloading the page."
      />
    )
  if (!isReady || !business || !query.data) return <ListSkeleton />
  const all = [...query.data.focusActions, ...query.data.additionalActions]
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Next actions"
        description="Prioritized, repeatable work based on OpenSight’s latest successful checks."
      />
      <FreshnessNotice freshness={query.data.freshness} />
      {selected.data?.action &&
        !all.some((action) => action.id === selected.data?.action?.id) && (
          <ActionSection
            title="Selected action history"
            actions={[selected.data.action]}
            mutation={mutation}
          />
        )}
      {all.length === 0 ? (
        <EmptyActions reason={query.data.emptyReason} />
      ) : (
        <>
          <ActionSection
            title="Focus now"
            actions={query.data.focusActions}
            mutation={mutation}
          />
          <ActionSection
            title="Additional recommendations"
            actions={query.data.additionalActions}
            mutation={mutation}
          />
        </>
      )}
    </div>
  )
}

function EmptyActions({ reason }: { reason: ActionsEmptyReason }) {
  const copy =
    reason === ActionsEmptyReason.HEALTHY
      ? [
          "All practices OpenSight can currently verify are in good standing",
          "Continuous practices may produce new action cycles as later checks change.",
        ]
      : reason === ActionsEmptyReason.INCOMPLETE_CHECKS
        ? [
            "Checks are incomplete",
            "OpenSight is preserving the last successful standings while incomplete checks are retried.",
          ]
        : [
            "Not enough assessment capability yet",
            "The visibility checklist shows every known practice, including those OpenSight cannot currently assess.",
          ]
  return (
    <SectionMessage icon={CheckCircle2} title={copy[0]} description={copy[1]} />
  )
}

function ActionSection({
  title,
  actions,
  mutation,
}: {
  title: string
  actions: ImprovementAction[]
  mutation: ReturnType<typeof useActionMutation>
}) {
  if (actions.length === 0) return null
  return (
    <section className="flex flex-col gap-3">
      <h2 className="font-heading text-xl font-medium">{title}</h2>
      {actions.map((action) => (
        <ActionCard key={action.id} action={action} mutation={mutation} />
      ))}
    </section>
  )
}

function ActionCard({
  action,
  mutation,
}: {
  action: ImprovementAction
  mutation: ReturnType<typeof useActionMutation>
}) {
  const [selectedEvidence, setSelectedEvidence] = useState<EvidenceSelection>()
  const update = (
    status: ActionStatus,
    dismissalReason: DismissalReason = DismissalReason.UNSPECIFIED
  ) => mutation.mutate({ actionId: action.id, status, dismissalReason })
  return (
    <>
      <Card id={`action-${action.id}`}>
        <CardHeader>
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">Cycle {action.cycle}</Badge>
            <Badge variant="secondary">{action.effort}</Badge>
            {!action.fresh && <Badge variant="outline">Stale check</Badge>}
          </div>
          <CardTitle>{action.title}</CardTitle>
          <CardDescription>{action.summary}</CardDescription>
        </CardHeader>
        <CardContent>
          <details className="group rounded-lg border px-4 py-3">
            <summary className="cursor-pointer font-medium">
              Action details
            </summary>
            <div className="mt-4 flex flex-col gap-4 text-sm">
              {action.blocks.map((block, index) => (
                <div key={`${block.type}-${index}`}>
                  {block.title && (
                    <h3 className="font-medium">{block.title}</h3>
                  )}
                  {block.text && (
                    <p className="mt-1 text-muted-foreground">{block.text}</p>
                  )}
                  {block.value && (
                    <p className="mt-1 font-medium">{block.value}</p>
                  )}
                  {block.resultIds.length > 0 && (
                    <Button
                      className="mt-2"
                      size="sm"
                      variant="outline"
                      onClick={() =>
                        setSelectedEvidence(
                          evidenceSelection(
                            block.resultIds,
                            block.title || action.title
                          )
                        )
                      }
                    >
                      View {block.resultIds.length} response
                      {block.resultIds.length === 1 ? "" : "s"}
                    </Button>
                  )}
                  {block.items.length > 0 && (
                    <ul className="mt-2 flex list-disc flex-col gap-1 pl-5">
                      {block.items.map((item) => (
                        <li key={item}>{item}</li>
                      ))}
                    </ul>
                  )}
                  {block.url && (
                    <a
                      className="mt-1 inline-flex items-center gap-1 text-primary underline"
                      href={block.url}
                      target="_blank"
                      rel="noreferrer"
                    >
                      {block.url} <ExternalLink className="size-3" />
                    </a>
                  )}
                </div>
              ))}
              {action.cycles.length > 0 && (
                <div>
                  <h3 className="font-medium">Action cycles</h3>
                  <ul className="mt-1 flex flex-col gap-1 text-muted-foreground">
                    {action.cycles.map((cycle) => (
                      <li key={cycle.id}>
                        Cycle {cycle.cycle} ·{" "}
                        {actionStatusLabels[cycle.status] ?? "Unknown"} ·
                        updated {formatDate(cycle.updatedAt)}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
          </details>
        </CardContent>
        <CardFooter className="flex flex-wrap gap-2">
          {action.status === ActionStatus.OPEN && (
            <Button
              disabled={mutation.isPending}
              onClick={() => update(ActionStatus.IN_PROGRESS)}
            >
              Start action
            </Button>
          )}
          {action.status === ActionStatus.IN_PROGRESS && (
            <Button
              disabled={mutation.isPending}
              onClick={() => update(ActionStatus.COMPLETED)}
            >
              Mark complete
            </Button>
          )}
          {(action.status === ActionStatus.OPEN ||
            action.status === ActionStatus.IN_PROGRESS) && (
            <Select
              onValueChange={(value) =>
                update(ActionStatus.DISMISSED, Number(value) as DismissalReason)
              }
            >
              <SelectTrigger className="w-44" aria-label="Dismiss action">
                <SelectValue placeholder="Dismiss…" />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value={String(DismissalReason.NOT_RELEVANT)}>
                    Not relevant
                  </SelectItem>
                  <SelectItem value={String(DismissalReason.ALREADY_DONE)}>
                    Already done
                  </SelectItem>
                  <SelectItem value={String(DismissalReason.NOT_ACTIONABLE)}>
                    Not actionable
                  </SelectItem>
                  <SelectItem value={String(DismissalReason.TOO_MUCH_EFFORT)}>
                    Too much effort
                  </SelectItem>
                  <SelectItem value={String(DismissalReason.OTHER)}>
                    Other
                  </SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          )}
          {action.status === ActionStatus.DISMISSED && (
            <Button
              variant="outline"
              disabled={mutation.isPending}
              onClick={() => update(ActionStatus.OPEN)}
            >
              Restore action
            </Button>
          )}
        </CardFooter>
      </Card>
      <ResponseDrawer
        evidence={selectedEvidence}
        onOpenChange={(open) => {
          if (!open) setSelectedEvidence(undefined)
        }}
      />
    </>
  )
}

export function ChecklistPage() {
  const { business, isError, isReady } = useCurrentBusiness()
  const query = useQuery(
    getChecklist,
    business ? { businessId: business.id } : skipToken
  )
  const [params, setParams] = useSearchParams()
  const [selectedEvidence, setSelectedEvidence] = useState<EvidenceSelection>()
  const selected = Number(params.get("standing") ?? 0) as ChecklistStanding
  if (isError || query.isError)
    return (
      <SectionMessage
        icon={ClipboardCheck}
        title="Checklist could not be loaded"
        description="Try reloading the page."
      />
    )
  if (!isReady || !business || !query.data) return <ListSkeleton />
  const sections = filterChecklistSections(query.data.sections, selected)
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Visibility checklist"
        description="Every catalog practice and the standing OpenSight can support with current evidence."
      />
      <FreshnessNotice freshness={query.data.freshness} />
      <div className="flex flex-wrap gap-2" aria-label="Filter by standing">
        <Button
          size="sm"
          variant={!selected ? "default" : "outline"}
          onClick={() => setParams({})}
        >
          All
        </Button>
        {query.data.counts.map((count) => (
          <Button
            key={count.standing}
            size="sm"
            variant={selected === count.standing ? "default" : "outline"}
            onClick={() => setParams({ standing: String(count.standing) })}
          >
            {standingLabels[count.standing]} {count.count}
          </Button>
        ))}
      </div>
      {sections.map((section) => (
        <section key={section.title} className="flex flex-col gap-3">
          <h2 className="font-heading text-xl font-medium">{section.title}</h2>
          {section.practices.map((practice) => (
            <Card key={practice.key} id={`practice-${practice.key}`}>
              <CardHeader>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <CardTitle>{practice.title}</CardTitle>
                  <Badge variant="outline">{practice.standingLabel}</Badge>
                </div>
                <CardDescription>{practice.description}</CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-3">
                {practice.subjects.map((subject) => (
                  <details
                    key={subject.subjectKey || "unassessed"}
                    className="rounded-lg border px-4 py-3"
                  >
                    <summary className="flex cursor-pointer list-none items-center justify-between gap-3">
                      <span className="font-medium">
                        {subject.label || practice.title}
                      </span>
                      <span className="flex items-center gap-2 text-sm text-muted-foreground">
                        {subject.stale && (
                          <Badge variant="outline">Stale</Badge>
                        )}
                        {subject.standingLabel}
                        <ChevronRight className="size-4" />
                      </span>
                    </summary>
                    <div className="mt-4 flex flex-col gap-3 text-sm">
                      <div>
                        <h3 className="font-medium">What OpenSight checks</h3>
                        <p className="text-muted-foreground">
                          {practice.description}
                        </p>
                      </div>
                      <div>
                        <h3 className="font-medium">Why it matters</h3>
                        <p className="text-muted-foreground">{practice.why}</p>
                      </div>
                      <div>
                        <h3 className="font-medium">
                          Current evidence and limits
                        </h3>
                        <p className="text-muted-foreground">
                          {subject.explanation}
                        </p>
                        <p className="mt-1 text-xs text-muted-foreground">
                          Last successful check:{" "}
                          {formatDate(subject.lastSuccessfulCheck)}
                        </p>
                      </div>
                      {subject.checkedSources.length > 0 && (
                        <div>
                          <h3 className="font-medium">Sources</h3>
                          {subject.checkedSources.map((source) => (
                            <a
                              key={source}
                              className="block break-all text-primary underline"
                              href={source}
                              target="_blank"
                              rel="noreferrer"
                            >
                              {source}
                            </a>
                          ))}
                        </div>
                      )}
                      {subject.resultIds.length > 0 && (
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() =>
                            setSelectedEvidence(
                              evidenceSelection(
                                subject.resultIds,
                                `${practice.title} evidence`
                              )
                            )
                          }
                        >
                          View {subject.resultIds.length} relevant response
                          {subject.resultIds.length === 1 ? "" : "s"}
                        </Button>
                      )}
                      {subject.currentAction && (
                        <Link
                          className="inline-flex items-center gap-1 text-primary underline"
                          to={`../actions?action=${subject.currentAction.id}`}
                        >
                          Current action: {subject.currentAction.title}
                        </Link>
                      )}
                      {subject.actionHistory.length > 0 && (
                        <div>
                          <h3 className="font-medium">Local action history</h3>
                          <ul className="mt-1 flex flex-col gap-1 text-xs text-muted-foreground">
                            {subject.actionHistory.map((cycle) => (
                              <li key={cycle.actionId}>
                                Cycle {cycle.cycle} ·{" "}
                                {actionStatusLabels[cycle.status] ?? "Unknown"}{" "}
                                · {formatDate(cycle.updatedAt)}
                              </li>
                            ))}
                          </ul>
                        </div>
                      )}
                    </div>
                  </details>
                ))}
              </CardContent>
            </Card>
          ))}
        </section>
      ))}
      <ResponseDrawer
        evidence={selectedEvidence}
        onOpenChange={(open) => {
          if (!open) setSelectedEvidence(undefined)
        }}
      />
    </div>
  )
}

export function ActivityPage() {
  const { business, isError, isReady } = useCurrentBusiness()
  const { accountSlug = "" } = useParams<{ accountSlug: string }>()
  const [params, setParams] = useSearchParams()
  const offset = Math.max(0, Number(params.get("offset") ?? 0))
  const limit = 20
  const query = useQuery(
    listActivity,
    business ? { businessId: business.id, limit, offset } : skipToken
  )
  if (isError || query.isError)
    return (
      <SectionMessage
        icon={Activity}
        title="Activity could not be loaded"
        description="Try reloading the page."
      />
    )
  if (!isReady || !business || !query.data) return <ListSkeleton />
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Activity history"
        description="Action lifecycle events in newest-first order. Later visibility changes are not attributed to these actions."
      />
      {query.data.events.length === 0 ? (
        <SectionMessage
          icon={CircleDashed}
          title="No action activity yet"
          description="Started, completed, dismissed, restored, retired, superseded, and recurring action cycles will appear here."
        />
      ) : (
        <ol className="relative ml-3 border-l">
          {query.data.events.map((event) => (
            <li key={event.id} className="relative mb-6 ml-6">
              <span className="absolute top-1 -left-[1.9rem] size-3 rounded-full border bg-background" />
              <p className="font-medium">
                {event.title} · {event.eventType}
              </p>
              <p className="text-sm text-muted-foreground">
                Cycle {event.cycle} · {formatDate(event.createdAt)}
              </p>
              <div className="mt-1 flex gap-3 text-sm">
                <Link
                  className="text-primary underline"
                  to={accountPath(
                    accountSlug,
                    `/improve/checklist#practice-${event.practiceKey}`
                  )}
                >
                  Checklist practice
                </Link>
                <Link
                  className="text-primary underline"
                  to={accountPath(
                    accountSlug,
                    `/improve/actions?action=${event.actionId}`
                  )}
                >
                  Action
                </Link>
              </div>
            </li>
          ))}
        </ol>
      )}
      <div className="flex justify-between">
        <Button
          variant="outline"
          disabled={offset === 0}
          onClick={() =>
            setParams({ offset: String(Math.max(0, offset - limit)) })
          }
        >
          Newer
        </Button>
        <Button
          variant="outline"
          disabled={offset + limit >= query.data.total}
          onClick={() => setParams({ offset: String(offset + limit) })}
        >
          Older
        </Button>
      </div>
    </div>
  )
}
