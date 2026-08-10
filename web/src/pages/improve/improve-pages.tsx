import { timestampDate } from "@bufbuild/protobuf/wkt"
import {
  createConnectQueryKey,
  skipToken,
  useMutation,
  useQuery,
} from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import {
  CheckCircle2,
  CircleDashed,
  ClipboardCheck,
  ExternalLink,
  HelpCircle,
  Info,
  Lightbulb,
  MinusCircle,
  XCircle,
} from "lucide-react"
import { useSearchParams } from "react-router"
import { useState } from "react"

import { useCurrentBusiness } from "@/api/hooks"
import {
  evidenceSelection,
  type EvidenceSelection,
} from "@/components/evidence-selection"
import { ListSkeleton } from "@/components/list-skeleton"
import { PageHeader } from "@/components/page-header"
import { ResponseDrawer } from "@/components/response-drawer"
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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  ActionsEmptyReason,
  ActionStatus,
  CheckOutcome,
  DismissalReason,
  type ActionCategory,
  type Check,
  type CheckCount,
  type CheckGroup,
  type ImprovementAction,
} from "@/gen/opensight/v1/improve_pb"
import {
  getAction,
  getChecklist,
  listActions,
  setActionStatus,
} from "@/gen/opensight/v1/improve-ImproveService_connectquery"

const checkOutcomeIcons: Record<
  number,
  { icon: typeof CheckCircle2; className: string }
> = {
  [CheckOutcome.PASS]: { icon: CheckCircle2, className: "text-emerald-600" },
  [CheckOutcome.FAIL]: { icon: XCircle, className: "text-destructive" },
  [CheckOutcome.COULD_NOT_VERIFY]: {
    icon: HelpCircle,
    className: "text-amber-600",
  },
  [CheckOutcome.NOT_APPLICABLE]: {
    icon: MinusCircle,
    className: "text-muted-foreground",
  },
  [CheckOutcome.NOT_ASSESSED]: {
    icon: CircleDashed,
    className: "text-muted-foreground",
  },
}

const checkCountLabels: Record<number, string> = {
  [CheckOutcome.PASS]: "checks passed",
  [CheckOutcome.FAIL]: "need attention",
  [CheckOutcome.COULD_NOT_VERIFY]: "could not be verified",
  [CheckOutcome.NOT_APPLICABLE]: "not applicable",
  [CheckOutcome.NOT_ASSESSED]: "not assessed yet",
}

function formatDate(value: Parameters<typeof timestampDate>[0] | undefined) {
  return value
    ? timestampDate(value).toLocaleString(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      })
    : "Not yet checked"
}

// A step may end with a preformatted block after a blank line — the
// structured-data action puts the JSON-LD to paste there. Rendering it as a
// code block is the point of generating it: collapsed into the sentence, the
// indentation and newlines the user has to copy would be lost.
function ActionStep({
  step,
  as: Tag = "li",
}: {
  step: string
  as?: "li" | "p"
}) {
  const split = step.indexOf("\n\n")
  if (split === -1) return <Tag>{step}</Tag>
  return (
    <Tag>
      {step.slice(0, split)}
      <pre className="mt-2 overflow-x-auto rounded-md border bg-muted p-3 text-xs">
        <code>{step.slice(split + 2)}</code>
      </pre>
    </Tag>
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
      ])
    },
  })
}

// CategoryFilter narrows the queue to one kind of change, because getting
// listed on somebody else's directory and editing your own site are different
// afternoons' work. It stays out of the way when there is only one kind.
function CategoryFilter({
  categories,
  category,
  onChange,
}: {
  categories: ActionCategory[]
  category: string
  onChange: (next: string) => void
}) {
  if (categories.length < 2) return null
  const total = categories.reduce((sum, entry) => sum + entry.count, 0)
  return (
    <div className="overflow-x-auto pb-1">
      <ToggleGroup
        value={[category || allCategories]}
        onValueChange={(values) => onChange(values[0] ?? allCategories)}
        size="sm"
        aria-label="Filter actions by kind of change"
      >
        <ToggleGroupItem value={allCategories} className="min-h-11">
          All {total}
        </ToggleGroupItem>
        {categories.map((entry) => (
          <ToggleGroupItem
            key={entry.key}
            value={entry.key}
            className="min-h-11"
          >
            {entry.label} {entry.count}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
    </div>
  )
}

// The sentinel the toggle group uses for "no filter". An empty string cannot be
// a toggle value, and it is never a real category key.
const allCategories = "all"

// contentCategory mirrors visibility.CategoryContent. Only these actions ask the
// user to write something, so only they carry the substantiation note.
const contentCategory = "content"

export function ActionsPage() {
  const { business, isError, isReady } = useCurrentBusiness()
  const query = useQuery(
    listActions,
    business ? { businessId: business.id } : skipToken
  )
  const mutation = useActionMutation(business?.id)
  const [params, setParams] = useSearchParams()
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

  const categories = query.data.categories
  // An unknown category in the URL is ignored rather than shown as an empty
  // queue: the categories a business has depend on its own evidence, so a
  // shared or stale link naming one it no longer has should still open.
  const requested = params.get("category") ?? ""
  const category = categories.some((entry) => entry.key === requested)
    ? requested
    : ""
  const setCategory = (next: string) =>
    setParams(
      (current) => {
        const updated = new URLSearchParams(current)
        if (next && next !== allCategories) updated.set("category", next)
        else updated.delete("category")
        return updated
      },
      { replace: true }
    )

  const matches = (action: ImprovementAction) =>
    !category || action.category === category
  const active = query.data.actions.filter(matches)
  const resolved = query.data.resolvedActions.filter(matches)
  const listed = [...active, ...resolved]
  const selectedAction = selected.data?.action
  const categoryLabel = categories.find(
    (entry) => entry.key === category
  )?.label
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Next actions"
        description="Prioritized work derived from your latest site audit and monitored answers."
      />
      {query.data.checkedAt && (
        <p className="text-sm text-muted-foreground">
          Evidence checked {formatDate(query.data.checkedAt)}
        </p>
      )}
      <CategoryFilter
        categories={categories}
        category={category}
        onChange={setCategory}
      />
      {selectedAction &&
        !listed.some((action) => action.id === selectedAction.id) && (
          <ActionSection
            title="Selected action"
            actions={[selectedAction]}
            mutation={mutation}
          />
        )}
      {active.length === 0 ? (
        <EmptyActions reason={query.data.emptyReason} />
      ) : (
        // One ranked list. The page header already names the queue, so an
        // unfiltered list needs no heading of its own.
        <ActionSection
          title={categoryLabel}
          actions={active}
          mutation={mutation}
        />
      )}
      {resolved.length > 0 && (
        <details className="rounded-xl border px-4 py-3">
          <summary className="cursor-pointer font-medium">
            Completed and dismissed ({resolved.length})
          </summary>
          <div className="mt-4 flex flex-col gap-3">
            {resolved.map((action) => (
              <ActionCard key={action.id} action={action} mutation={mutation} />
            ))}
          </div>
        </details>
      )}
    </div>
  )
}

function EmptyActions({ reason }: { reason: ActionsEmptyReason }) {
  if (reason === ActionsEmptyReason.NOT_ASSESSED_YET) {
    return (
      <SectionMessage
        icon={CircleDashed}
        title="No findings yet"
        description="OpenSight has not completed the first site audit and evidence review yet."
      />
    )
  }
  return (
    <SectionMessage
      icon={CheckCircle2}
      title="No actions found in the latest evidence"
      description="This is not a guarantee that nothing can be improved. New monitored answers or a later site audit may reveal useful work."
    />
  )
}

function ActionSection({
  title,
  actions,
  mutation,
}: {
  title?: string
  actions: ImprovementAction[]
  mutation: ReturnType<typeof useActionMutation>
}) {
  if (actions.length === 0) return null
  return (
    <section className="flex flex-col gap-3">
      {title && <h2 className="font-heading text-xl font-medium">{title}</h2>}
      {actions.map((action) => (
        <ActionCard key={action.id} action={action} mutation={mutation} />
      ))}
    </section>
  )
}

// hostOf renders a source URL as the domain a reader recognizes. A card lists
// the pages we read, and five wrapped absolute URLs bury the one fact that
// identifies them.
function hostOf(url: string) {
  try {
    return new URL(url).hostname.replace(/^www\./, "")
  } catch {
    return url
  }
}

const panelHeading =
  "text-xs font-medium tracking-wide text-muted-foreground uppercase"

const quoteStyle = "border-l-2 pl-3 text-muted-foreground italic"

// EvidenceComparison is the argument for a content action: what answers said
// about competitors, and what the customer's own site says. Both halves are
// quotations, because the claim being made — that a recommendation went
// somewhere else over something they do not say — is one the user must be able
// to check rather than take on trust.
//
// Two panels only when there are two things to compare. With no site passage to
// quote there is no second side, and a panel the width of the evidence holding
// one sentence reads as a rendering fault rather than as an absence; the verdict
// becomes a line under the evidence instead.
function EvidenceComparison({
  comparison,
  caption,
}: {
  comparison: NonNullable<ImprovementAction["comparison"]>
  caption: string
}) {
  const cited = comparison.cited
  const site = comparison.site
  if (cited.length === 0 && site.length === 0) return null
  const paired = cited.length > 0 && site.length > 0
  return (
    <div className={paired ? "grid gap-3 sm:grid-cols-2" : "flex flex-col"}>
      {cited.length > 0 && (
        <section className="rounded-lg border bg-muted/40 p-3">
          <h3 className={panelHeading}>What ChatGPT said instead</h3>
          <ul className="mt-2 flex flex-col gap-3">
            {cited.map((quote) => (
              <li key={quote.quote}>
                <blockquote className={quoteStyle}>{quote.quote}</blockquote>
                <p className="mt-1 pl-3 text-xs text-muted-foreground">
                  cited {quote.domain}
                </p>
              </li>
            ))}
          </ul>
          {caption && (
            <p className="mt-3 text-xs text-muted-foreground">{caption}</p>
          )}
        </section>
      )}
      {site.length > 0 ? (
        <section className="rounded-lg border p-3">
          <h3 className={panelHeading}>What your site says</h3>
          <ul className="mt-2 flex flex-col gap-3">
            {site.map((quote) => (
              <li key={quote}>
                <blockquote className={quoteStyle}>{quote}</blockquote>
              </li>
            ))}
          </ul>
          {comparison.coverage === "partial" && (
            <p className="mt-3 text-xs text-muted-foreground">
              Closest wording we found, and it stops short of the detail cited
              alongside.
            </p>
          )}
        </section>
      ) : (
        <p className="mt-3 flex flex-wrap items-baseline gap-x-2 px-3">
          <span className={panelHeading}>What your site says</span>
          <span className="text-sm font-medium">
            {comparison.coverage === "partial"
              ? "Covered, but not with the detail cited alongside"
              : "Nothing on this subject"}
          </span>
        </p>
      )}
    </div>
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
  const hosts = [...new Set(action.sources.map(hostOf))]

  return (
    <>
      <Card id={`action-${action.id}`}>
        <CardHeader>
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            {action.blocking && (
              <Badge variant="destructive" className="text-xs">
                Blocking
              </Badge>
            )}
            {action.categoryLabel && <span>{action.categoryLabel}</span>}
            {action.reach > 0 && (
              <>
                <span aria-hidden>·</span>
                <span className="font-medium text-foreground">
                  {action.reach} answer{action.reach === 1 ? "" : "s"} affected
                </span>
              </>
            )}
          </div>
          <CardTitle>{action.title}</CardTitle>
          <CardDescription>{action.body}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4 text-sm">
          {action.comparison && (
            <EvidenceComparison
              comparison={action.comparison}
              caption={action.detail}
            />
          )}
          {!action.comparison && action.detail && (
            <div>
              <h3 className="font-medium">What we found</h3>
              <p className="mt-1 text-muted-foreground">{action.detail}</p>
            </div>
          )}
          {action.steps.length > 0 && (
            <div>
              <h3 className="font-medium">Do this</h3>
              {action.steps.length === 1 ? (
                <div className="mt-1 text-muted-foreground">
                  <ActionStep step={action.steps[0]} as="p" />
                </div>
              ) : (
                <ol className="mt-2 flex list-decimal flex-col gap-1.5 pl-5">
                  {action.steps.map((step) => (
                    <ActionStep key={step} step={step} />
                  ))}
                </ol>
              )}
              {/* Substantiation is a constraint on how every website-content
                  change is written, not a second task. Numbered beside the
                  recommendation it read as half the work; it stays on the card
                  so a filtered or deep-linked view never drops it. */}
              {action.category === contentCategory && (
                <p className="mt-2 flex items-start gap-1.5 text-xs text-muted-foreground">
                  <Info aria-hidden className="mt-0.5 size-3.5 shrink-0" />
                  Publish only facts you can substantiate. Do not copy another
                  business&rsquo;s wording or imply outcomes you cannot support.
                </p>
              )}
            </div>
          )}
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            {action.resultIds.length > 0 && (
              <Button
                size="sm"
                variant="outline"
                onClick={() =>
                  setSelectedEvidence(
                    evidenceSelection(action.resultIds, action.title)
                  )
                }
              >
                View {action.resultIds.length} answer
                {action.resultIds.length === 1 ? "" : "s"}
              </Button>
            )}
            {hosts.length > 0 && (
              <details className="text-xs text-muted-foreground">
                <summary className="cursor-pointer select-none">
                  {hosts.slice(0, 2).join(", ")}
                  {hosts.length > 2 ? ` +${hosts.length - 2}` : ""} checked
                </summary>
                <ul className="mt-2 flex flex-col gap-1">
                  {action.sources.map((source) => (
                    <li key={source}>
                      <a
                        className="inline-flex items-center gap-1 break-all text-primary underline"
                        href={source}
                        target="_blank"
                        rel="noreferrer"
                      >
                        {source} <ExternalLink className="size-3 shrink-0" />
                      </a>
                    </li>
                  ))}
                </ul>
              </details>
            )}
          </div>
        </CardContent>
        <CardFooter className="flex flex-wrap gap-2">
          {action.status === ActionStatus.OPEN && (
            <>
              <Button
                disabled={mutation.isPending}
                onClick={() => update(ActionStatus.DONE)}
              >
                Mark done
              </Button>
              <Select
                onValueChange={(value) =>
                  update(
                    ActionStatus.DISMISSED,
                    Number(value) as DismissalReason
                  )
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
            </>
          )}
          {action.status !== ActionStatus.OPEN && (
            <Button
              variant="outline"
              disabled={mutation.isPending}
              onClick={() => update(ActionStatus.OPEN)}
            >
              Reopen action
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

function CheckRow({ check }: { check: Check }) {
  const { icon: Icon, className } =
    checkOutcomeIcons[check.outcome] ??
    checkOutcomeIcons[CheckOutcome.NOT_ASSESSED]
  return (
    <li className="flex items-start gap-3 px-(--card-spacing) py-3">
      <Icon
        aria-hidden
        className={`mt-0.5 size-4 shrink-0 ${check.informational ? "text-muted-foreground" : className}`}
      />
      <div className="min-w-0 flex-1">
        <p className="font-medium">{check.title}</p>
        <details className="mt-1 text-sm text-muted-foreground">
          <summary className="w-fit cursor-pointer underline underline-offset-2 select-none marker:text-muted-foreground/60 hover:text-foreground">
            What does this mean?
          </summary>
          <div className="mt-2 space-y-2 border-l-2 pl-3">
            <p>{check.what}</p>
            {check.detail && (
              <p>
                <span className="font-medium text-foreground">
                  What we found:
                </span>
                {check.detail}
              </p>
            )}
          </div>
        </details>
      </div>
      <span className="shrink-0 text-xs text-muted-foreground">
        {check.outcomeLabel}
      </span>
    </li>
  )
}

function CheckTotals({ counts }: { counts: CheckCount[] }) {
  if (counts.length === 0) return null
  return (
    <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
      {counts.map((count) => {
        const { icon: Icon, className } =
          checkOutcomeIcons[count.outcome] ??
          checkOutcomeIcons[CheckOutcome.NOT_ASSESSED]
        return (
          <span key={count.outcome} className="flex items-center gap-2">
            <Icon aria-hidden className={`size-4 ${className}`} />
            <span className="font-medium">{count.count}</span>
            <span className="text-muted-foreground">
              {checkCountLabels[count.outcome] ?? "counted"}
            </span>
          </span>
        )
      })}
    </div>
  )
}

function checkSummary(passed: number, total: number) {
  if (total === 0) return ""
  if (passed === total)
    return `all ${total} check${total === 1 ? "" : "s"} passed`
  return `${passed} of ${total} checks passed`
}

function CheckGroupCard({ group }: { group: CheckGroup }) {
  return (
    <Card id={`check-group-${group.key}`}>
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-1">
          <CardTitle>{group.title}</CardTitle>
          <span className="shrink-0 text-xs text-muted-foreground">
            {checkSummary(group.checksPassed, group.checksTotal)}
          </span>
        </div>
        <CardDescription>{group.description}</CardDescription>
      </CardHeader>
      <CardContent className="px-0">
        <ul className="divide-y border-y">
          {group.checks.map((check) => (
            <CheckRow key={check.key} check={check} />
          ))}
        </ul>
      </CardContent>
      {group.references.length > 0 && (
        <CardFooter className="flex flex-wrap gap-3 text-xs">
          {group.references.map((reference) => (
            <a
              key={reference}
              className="inline-flex items-center gap-1 text-primary underline"
              href={reference}
              target="_blank"
              rel="noreferrer"
            >
              Reference <ExternalLink className="size-3" />
            </a>
          ))}
        </CardFooter>
      )}
    </Card>
  )
}

export function ChecklistPage() {
  const { business, isError, isReady } = useCurrentBusiness()
  const query = useQuery(
    getChecklist,
    business ? { businessId: business.id } : skipToken
  )

  if (isError || query.isError)
    return (
      <SectionMessage
        icon={ClipboardCheck}
        title="Checklist could not be loaded"
        description="Try reloading the page."
      />
    )
  if (!isReady || !business || !query.data) return <ListSkeleton />

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-4">
        <PageHeader
          title="Visibility checklist"
          description="Everything OpenSight tests on your site, and what the latest audit found."
        />
        <CheckTotals counts={query.data.checkCounts} />
      </div>
      {!query.data.assessed && (
        <Alert>
          <AlertDescription>
            The first site audit has not completed yet. The full checklist is
            shown below so you can see what will be tested.
          </AlertDescription>
        </Alert>
      )}
      {query.data.failure && (
        <Alert>
          <AlertDescription>
            The site could not be audited: {query.data.failure}. Every check is
            shown as unverified instead of being treated as a failure.
          </AlertDescription>
        </Alert>
      )}
      {query.data.assessed && (
        <p className="text-sm text-muted-foreground">
          Checked {formatDate(query.data.checkedAt)} · {query.data.pagesRead}{" "}
          page{query.data.pagesRead === 1 ? "" : "s"} read
        </p>
      )}
      {query.data.groups.map((group) => (
        <CheckGroupCard key={group.key} group={group} />
      ))}
    </div>
  )
}
