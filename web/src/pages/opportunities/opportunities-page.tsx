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

  const focus = query.data.opportunities.filter(
    (item) => item.focus && item.status !== OpportunityStatus.DISMISSED
  )
  const completed = query.data.opportunities.filter(
    (item) => item.status === OpportunityStatus.COMPLETED
  )
  const dismissed = query.data.opportunities.filter(
    (item) => item.status === OpportunityStatus.DISMISSED
  )
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
      {focus.length === 0 ? (
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
      ) : (
        <section className="flex flex-col gap-3" aria-labelledby="focus-title">
          <h2 id="focus-title" className="font-heading text-xl font-medium">
            Focus now
          </h2>
          {focus.map((item, index) => (
            <OpportunityCard
              key={item.id}
              item={item}
              direct={
                index === 0 &&
                item.practiceKey === "discoverability.openai_search_access"
              }
              pending={mutation.isPending}
              update={update}
            />
          ))}
        </section>
      )}
      {completed.length > 0 && (
        <section
          className="flex flex-col gap-3"
          aria-labelledby="completed-title"
        >
          <h2 id="completed-title" className="font-heading text-xl font-medium">
            Completed, awaiting observation
          </h2>
          {completed.map((item) => (
            <OpportunityCard
              key={item.id}
              item={item}
              pending={mutation.isPending}
              update={update}
            />
          ))}
        </section>
      )}
      {dismissed.length > 0 && (
        <section
          className="flex flex-col gap-3"
          aria-labelledby="dismissed-title"
        >
          <h2 id="dismissed-title" className="font-heading text-xl font-medium">
            Dismissed
          </h2>
          {dismissed.map((item) => (
            <OpportunityCard
              key={item.id}
              item={item}
              pending={mutation.isPending}
              update={update}
            />
          ))}
        </section>
      )}
    </div>
  )
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
          <Badge variant="outline">
            {Math.round(item.confidence * 100)}% confidence
          </Badge>
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
