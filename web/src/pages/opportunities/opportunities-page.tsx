import {
  createConnectQueryKey,
  skipToken,
  useMutation,
  useQuery,
} from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { CircleCheck, ExternalLink, Lightbulb, Play } from "lucide-react"

import { useCurrentBusiness } from "@/api/hooks"
import { PageHeader } from "@/components/page-header"
import { SectionMessage } from "@/components/section-message"
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
import type { Opportunity } from "@/gen/opensight/v1/opportunity_pb"
import {
  DismissalReason,
  OpportunityBlockType,
  OpportunityStatus,
} from "@/gen/opensight/v1/opportunity_pb"
import {
  listOpportunities,
  setOpportunityStatus,
} from "@/gen/opensight/v1/opportunity-OpportunityService_connectquery"
import { ListSkeleton } from "@/components/list-skeleton"

const dismissals = [
  [DismissalReason.NOT_RELEVANT, "Not relevant"],
  [DismissalReason.ALREADY_DONE, "Already done"],
  [DismissalReason.NOT_ACTIONABLE, "Not actionable"],
  [DismissalReason.TOO_MUCH_EFFORT, "Too much effort"],
  [DismissalReason.OTHER, "Other"],
] as const

export function OpportunitiesPage() {
  const { business, isError, isReady } = useCurrentBusiness()
  const queryClient = useQueryClient()
  const query = useQuery(
    listOpportunities,
    business ? { businessId: business.id } : skipToken
  )
  const mutation = useMutation(setOpportunityStatus, {
    onSuccess: () =>
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: listOpportunities,
          input: business ? { businessId: business.id } : undefined,
          cardinality: "finite",
        }),
      }),
  })

  if (isError || query.isError)
    return (
      <SectionMessage
        icon={Lightbulb}
        title="Something went wrong"
        description="Opportunities could not be loaded. Try reloading the page."
      />
    )
  if (!isReady || !business || !query.data) return <ListSkeleton />

  const buckets = groupIntoSections(query.data.opportunities)
  const update = (
    item: Opportunity,
    status: OpportunityStatus,
    dismissalReason: DismissalReason = DismissalReason.UNSPECIFIED
  ) => mutation.mutate({ opportunityId: item.id, status, dismissalReason })

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Opportunities"
        description="A small, evidence-backed queue of visibility work worth considering now."
      />
      {buckets.focus.length === 0 && (
        <Empty>
          <EmptyMedia variant="icon">
            <CircleCheck />
          </EmptyMedia>
          <EmptyHeader>
            <EmptyTitle>No current focus items</EmptyTitle>
            <EmptyDescription>
              OpenSight has not found an active unmet practice it can verify
              yet. This does not mean every possible visibility practice is
              complete.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      )}
      {sections.map(({ key, title, description }) =>
        buckets[key].length === 0 ? null : (
          <section
            key={key}
            className="flex flex-col gap-3"
            aria-labelledby={`${key}-title`}
          >
            <div className="flex flex-col gap-1">
              <h2
                id={`${key}-title`}
                className="font-heading text-xl font-medium"
              >
                {title}
              </h2>
              {description && (
                <p className="text-sm text-muted-foreground">{description}</p>
              )}
            </div>
            {buckets[key].map((item, index) => (
              <OpportunityCard
                key={item.id}
                item={item}
                direct={key === "focus" && index === 0 && item.directBlocker}
                pending={mutation.isPending}
                update={update}
              />
            ))}
          </section>
        )
      )}
    </div>
  )
}

const sections = [
  { key: "focus", title: "Focus now", description: "" },
  {
    key: "more",
    title: "More opportunities",
    description:
      "Also detected right now, ranked below your focus items. They move up as focus items are completed or dismissed.",
  },
  {
    key: "resolved",
    title: "No longer detected",
    description:
      "You acted on these and the latest check no longer detects the issue. That is an observation, not proof that your work caused the change.",
  },
  {
    key: "completed",
    title: "Completed",
    description:
      "You marked these complete. The latest check still detects the issue, which can lag behind a change.",
  },
  { key: "dismissed", title: "Dismissed", description: "" },
] as const

type SectionKey = (typeof sections)[number]["key"]

// Every opportunity the API returns lands in exactly one section: the switch is
// total over (status, current, focus), so nothing can be silently invisible. An
// item that stopped being current and that the user never touched is not
// returned by the API at all.
function sectionOf(item: Opportunity): SectionKey {
  if (item.status === OpportunityStatus.DISMISSED) return "dismissed"
  if (!item.current) return "resolved"
  if (item.status === OpportunityStatus.COMPLETED) return "completed"
  return item.focus ? "focus" : "more"
}

function groupIntoSections(items: Opportunity[]) {
  const buckets = Object.fromEntries(
    sections.map(({ key }) => [key, [] as Opportunity[]])
  ) as Record<SectionKey, Opportunity[]>
  for (const item of items) buckets[sectionOf(item)].push(item)
  return buckets
}

function OpportunityCard({
  item,
  direct = false,
  pending,
  update,
}: {
  item: Opportunity
  direct?: boolean
  pending: boolean
  update: (
    item: Opportunity,
    status: OpportunityStatus,
    reason?: DismissalReason
  ) => void
}) {
  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-center gap-2">
          {direct && <Badge variant="destructive">Fix first</Badge>}
          <Badge variant="secondary">{item.effort} effort</Badge>
        </div>
        <CardTitle>{item.title}</CardTitle>
        <CardDescription>{item.summary}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {item.blocks.map((block, index) => {
          if (block.type === OpportunityBlockType.LINK)
            return (
              <a
                key={index}
                href={block.url}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1 text-sm underline underline-offset-4"
              >
                {block.title || "Checked source"}
                <ExternalLink />
              </a>
            )
          if (
            block.type === OpportunityBlockType.QUESTION_LIST ||
            block.type === OpportunityBlockType.EVIDENCE_LIST
          )
            return (
              <div key={index} className="flex flex-col gap-2">
                <h3 className="font-medium">{block.title}</h3>
                <ul className="list-disc pl-5 text-sm text-muted-foreground">
                  {block.items.map((value) => (
                    <li key={value}>{value}</li>
                  ))}
                </ul>
              </div>
            )
          if (block.type === OpportunityBlockType.METRIC)
            return (
              <div key={index} className="flex items-baseline gap-2">
                <span className="font-heading text-2xl font-semibold">
                  {block.value}
                </span>
                <span className="text-sm text-muted-foreground">
                  {block.title}
                </span>
              </div>
            )
          return (
            <div key={index} className="flex flex-col gap-1">
              <h3 className="font-medium">{block.title}</h3>
              <p className="text-sm text-muted-foreground">{block.text}</p>
            </div>
          )
        })}
        {item.observations.length > 0 && (
          <div className="flex flex-col gap-2">
            <h3 className="font-medium">Later observations</h3>
            {item.observations.map((observation) => (
              <p
                key={`${observation.observedAt}-${observation.assessmentStatus}`}
                className="text-sm text-muted-foreground"
              >
                {new Date(observation.observedAt).toLocaleDateString()}: the
                practice was assessed as{" "}
                {observation.assessmentStatus
                  .toLowerCase()
                  .replaceAll("_", " ")}
                . This is an observation, not proof that the completed action
                caused a change.
              </p>
            ))}
          </div>
        )}
      </CardContent>
      <CardFooter className="flex flex-wrap gap-2">
        {item.status === OpportunityStatus.OPEN && (
          <Button
            disabled={pending}
            onClick={() => update(item, OpportunityStatus.IN_PROGRESS)}
          >
            <Play data-icon="inline-start" />
            Start
          </Button>
        )}
        {item.status === OpportunityStatus.IN_PROGRESS && (
          <Button
            disabled={pending}
            onClick={() => update(item, OpportunityStatus.COMPLETED)}
          >
            <CircleCheck data-icon="inline-start" />
            Mark complete
          </Button>
        )}
        {(item.status === OpportunityStatus.COMPLETED ||
          item.status === OpportunityStatus.DISMISSED) && (
          <Button
            variant="outline"
            disabled={pending}
            onClick={() => update(item, OpportunityStatus.OPEN)}
          >
            Restore
          </Button>
        )}
        {item.status !== OpportunityStatus.DISMISSED && (
          <Select
            onValueChange={(value) =>
              update(
                item,
                OpportunityStatus.DISMISSED,
                Number(value) as DismissalReason
              )
            }
          >
            <SelectTrigger aria-label="Dismiss opportunity">
              <SelectValue placeholder="Dismiss…" />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {dismissals.map(([value, label]) => (
                  <SelectItem key={value} value={String(value)}>
                    {label}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        )}
      </CardFooter>
    </Card>
  )
}
