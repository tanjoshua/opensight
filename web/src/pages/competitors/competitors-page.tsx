import { ChevronDown, ChevronRight, Plus, Users } from "lucide-react"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { useNavigate, useSearchParams } from "react-router"
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts"

import { useMe } from "@/api/auth"
import { ApiError } from "@/api/client"
import {
  useAddCompetitor,
  useAllCompetitors,
  useReviewSuggestedAlias,
  useSetCompetitorStatus,
  type AddCompetitorInput,
  type Competitor,
  type CompetitorPromptAppearance,
  type CompetitorSelf,
  type CompetitorStatus,
  type CompetitorTrendPoint,
} from "@/api/competitors"
import { ResponseDrawer } from "@/components/response-drawer"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  ChartContainer,
  ChartTooltip,
  type ChartConfig,
} from "@/components/ui/chart"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"

const chartConfig = {
  percent: { label: "Coverage", color: "var(--primary)" },
} satisfies ChartConfig

export function CompetitorsPage() {
  const me = useMe()
  const business = me.data?.businesses[0]
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const focus = statusParam(searchParams.get("status"))
  const competitorsQuery = useAllCompetitors(business?.id)
  const addCompetitor = useAddCompetitor(business?.id)
  const setStatus = useSetCompetitorStatus(business?.id)
  const reviewAlias = useReviewSuggestedAlias(business?.id)
  const [selectedResultID, setSelectedResultID] = useState<string>()
  const [addOpen, setAddOpen] = useState(false)
  const openResult = (ids: string[]) => {
    if (ids.length > 0) setSelectedResultID(ids[0])
  }

  if (me.isError || competitorsQuery.isError) {
    return (
      <SectionMessage
        title="Something went wrong"
        description="The competitors could not be loaded. Try reloading the page."
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
  if (!competitorsQuery.data) {
    return <ListSkeleton />
  }

  const { self, competitors } = competitorsQuery.data
  const discovered = competitors.filter((c) => c.status === "discovered")
  const tracked = competitors.filter((c) => c.status === "tracked")
  const dismissed = competitors.filter((c) => c.status === "dismissed")
  const statusError =
    setStatus.error instanceof ApiError
      ? setStatus.error.message
      : setStatus.isError
        ? "Could not update the competitor. Try again."
        : undefined
  const addError =
    addCompetitor.error instanceof ApiError
      ? addCompetitor.error.message
      : addCompetitor.isError
        ? "Could not add the competitor. Try again."
        : undefined
  const aliasError =
    reviewAlias.error instanceof ApiError
      ? reviewAlias.error.message
      : reviewAlias.isError
        ? "Could not review the suggested alias. Refresh and try again."
        : undefined
  const pendingCompetitorID = setStatus.isPending
    ? setStatus.variables.competitorId
    : undefined
  const changeStatus = (
    competitorId: string,
    status: "tracked" | "dismissed"
  ) => setStatus.mutate({ competitorId, status })
  const changeAlias = (
    competitorId: string,
    alias: string,
    action: "approve" | "reject"
  ) => reviewAlias.mutate({ competitorId, alias, action })
  const pendingAlias =
    reviewAlias.isPending && reviewAlias.variables
      ? `${reviewAlias.variables.competitorId}\u0000${reviewAlias.variables.alias}`
      : undefined
  const changeAddOpen = (open: boolean) => {
    setAddOpen(open)
    if (!open) addCompetitor.reset()
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-2">
          <h1 className="font-heading text-lg font-semibold">Competitors</h1>
          <Button onClick={() => changeAddOpen(true)}>
            <Plus data-icon="inline-start" />
            Add competitor
          </Button>
        </div>
        <SelfBaseline self={self} onOpenResult={openResult} />
      </div>

      <AddCompetitorDialog
        open={addOpen}
        onOpenChange={changeAddOpen}
        submitting={addCompetitor.isPending}
        errorMessage={addError}
        onSubmit={(input) =>
          addCompetitor.mutate(input, {
            onSuccess: () => setAddOpen(false),
          })
        }
      />

      {(statusError || aliasError) && (
        <p className="text-sm text-destructive" role="alert">
          {statusError ?? aliasError}
        </p>
      )}

      <DiscoveredSection
        competitors={discovered}
        self={self}
        focus={focus === "discovered"}
        onOpenResult={openResult}
        onStatusChange={changeStatus}
        pendingCompetitorID={pendingCompetitorID}
        onReviewAlias={changeAlias}
        pendingAlias={pendingAlias}
      />
      <TrackedSection
        competitors={tracked}
        focus={focus === "tracked"}
        onOpenResult={openResult}
        onSelectRun={(runID) => navigate(`/responses?run=${runID}`)}
        onStatusChange={changeStatus}
        pendingCompetitorID={pendingCompetitorID}
        onReviewAlias={changeAlias}
        pendingAlias={pendingAlias}
      />
      <DismissedSection
        competitors={dismissed}
        self={self}
        focus={focus === "dismissed"}
        onOpenResult={openResult}
        onStatusChange={changeStatus}
        pendingCompetitorID={pendingCompetitorID}
        onReviewAlias={changeAlias}
        pendingAlias={pendingAlias}
      />

      <ResponseDrawer
        resultId={selectedResultID}
        onOpenChange={(open) => {
          if (!open) setSelectedResultID(undefined)
        }}
      />
    </div>
  )
}

function SelfBaseline({
  self,
  onOpenResult,
}: {
  self: CompetitorSelf
  onOpenResult: (ids: string[]) => void
}) {
  const disabled = self.result_ids.length === 0
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={() => onOpenResult(self.result_ids)}
      title="Open a response behind this number"
      className="rounded-lg border px-4 py-2 text-left enabled:cursor-pointer enabled:hover:bg-muted/50 disabled:opacity-70"
    >
      <div className="text-xs text-muted-foreground">
        You appear in {self.mentioned} of {self.total_analyzed} responses
      </div>
      <div className="text-2xl font-semibold tabular-nums">
        {formatPercent(self.percent)}
      </div>
    </button>
  )
}

function DiscoveredSection({
  competitors,
  self,
  focus,
  onOpenResult,
  onStatusChange,
  pendingCompetitorID,
  onReviewAlias,
  pendingAlias,
}: {
  competitors: Competitor[]
  self: CompetitorSelf
  focus: boolean
  onOpenResult: (ids: string[]) => void
  onStatusChange: (
    competitorId: string,
    status: "tracked" | "dismissed"
  ) => void
  pendingCompetitorID?: string
  onReviewAlias: (
    competitorId: string,
    alias: string,
    action: "approve" | "reject"
  ) => void
  pendingAlias?: string
}) {
  const ref = useScrollIntoView<HTMLDivElement>(focus)
  return (
    <section ref={ref} className="flex flex-col gap-2">
      <SectionHeading
        title="Discovered"
        description="Named in your responses, not yet triaged — ranked by coverage."
      />
      {competitors.length === 0 ? (
        <EmptyNote>No discovered competitors — all triaged.</EmptyNote>
      ) : (
        <div className="flex flex-col gap-1.5">
          {competitors.map((competitor) => (
            <CoverageRow
              key={competitor.id}
              competitor={competitor}
              total={self.total_analyzed}
              onOpenResult={onOpenResult}
              aliasReview={
                <SuggestedAliasReview
                  competitor={competitor}
                  onReview={onReviewAlias}
                  pendingAlias={pendingAlias}
                />
              }
              actions={
                <>
                  <Button
                    size="sm"
                    aria-label={`Track ${competitor.name}`}
                    disabled={pendingCompetitorID === competitor.id}
                    onClick={() => onStatusChange(competitor.id, "tracked")}
                  >
                    Track
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    aria-label={`Dismiss ${competitor.name}`}
                    disabled={pendingCompetitorID === competitor.id}
                    onClick={() => onStatusChange(competitor.id, "dismissed")}
                  >
                    Dismiss
                  </Button>
                </>
              }
            />
          ))}
        </div>
      )}
    </section>
  )
}

function CoverageRow({
  competitor,
  total,
  onOpenResult,
  actions,
  aliasReview,
}: {
  competitor: Competitor
  total: number
  onOpenResult: (ids: string[]) => void
  actions?: ReactNode
  aliasReview?: ReactNode
}) {
  const disabled = competitor.result_ids.length === 0
  return (
    <div className="flex w-full flex-col gap-2 rounded-md border px-3 py-2">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="truncate font-medium">{competitor.name}</span>
          <button
            type="button"
            disabled={disabled}
            onClick={() => onOpenResult(competitor.result_ids)}
            title="Open a response behind this number"
            className="flex items-center gap-3 self-start rounded text-left text-sm text-muted-foreground enabled:cursor-pointer enabled:hover:text-foreground disabled:opacity-70"
          >
            <span className="tabular-nums">
              in {competitor.mentioned} of {total} responses
            </span>
            <Badge variant="outline" className="tabular-nums">
              {formatPercent(competitor.mention_percent)}
            </Badge>
          </button>
        </div>
        {actions && (
          <div className="flex shrink-0 items-center gap-2">{actions}</div>
        )}
      </div>
      {aliasReview}
    </div>
  )
}

function TrackedSection({
  competitors,
  focus,
  onOpenResult,
  onSelectRun,
  onStatusChange,
  pendingCompetitorID,
  onReviewAlias,
  pendingAlias,
}: {
  competitors: Competitor[]
  focus: boolean
  onOpenResult: (ids: string[]) => void
  onSelectRun: (runID: string) => void
  onStatusChange: (
    competitorId: string,
    status: "tracked" | "dismissed"
  ) => void
  pendingCompetitorID?: string
  onReviewAlias: (
    competitorId: string,
    alias: string,
    action: "approve" | "reject"
  ) => void
  pendingAlias?: string
}) {
  const ref = useScrollIntoView<HTMLDivElement>(focus)
  return (
    <section ref={ref} className="flex flex-col gap-2">
      <SectionHeading
        title="Tracked"
        description="How each tracked competitor compares to you."
      />
      {competitors.length === 0 ? (
        <EmptyNote>
          No tracked competitors yet. Track a discovered competitor to compare
          it here.
        </EmptyNote>
      ) : (
        <div className="grid gap-4 lg:grid-cols-2">
          {competitors.map((competitor) => (
            <TrackedCard
              key={competitor.id}
              competitor={competitor}
              onOpenResult={onOpenResult}
              onSelectRun={onSelectRun}
              onDismiss={() => onStatusChange(competitor.id, "dismissed")}
              statusPending={pendingCompetitorID === competitor.id}
              onReviewAlias={onReviewAlias}
              pendingAlias={pendingAlias}
            />
          ))}
        </div>
      )}
    </section>
  )
}

function TrackedCard({
  competitor,
  onOpenResult,
  onSelectRun,
  onDismiss,
  statusPending,
  onReviewAlias,
  pendingAlias,
}: {
  competitor: Competitor
  onOpenResult: (ids: string[]) => void
  onSelectRun: (runID: string) => void
  onDismiss: () => void
  statusPending: boolean
  onReviewAlias: (
    competitorId: string,
    alias: string,
    action: "approve" | "reject"
  ) => void
  pendingAlias?: string
}) {
  const openOwn = () => onOpenResult(competitor.result_ids)
  const hasEvidence = competitor.result_ids.length > 0
  return (
    <Card className="gap-3">
      <CardHeader className="flex-row items-start justify-between gap-2">
        <div className="flex flex-col gap-1.5">
          <CardTitle className="text-base">{competitor.name}</CardTitle>
          <CardDescription>
            Mentioned in {competitor.mentioned} responses
          </CardDescription>
        </div>
        <Button
          size="sm"
          variant="outline"
          aria-label={`Dismiss ${competitor.name}`}
          disabled={statusPending}
          onClick={onDismiss}
        >
          Dismiss
        </Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
          <Stat label="Mention %" onClick={openOwn} disabled={!hasEvidence}>
            <span className="text-2xl font-semibold tabular-nums">
              {formatPercent(competitor.mention_percent)}
            </span>
          </Stat>
          <Stat label="vs you" onClick={openOwn} disabled={!hasEvidence}>
            <VsSelf vsSelf={competitor.vs_self} />
          </Stat>
          <Stat
            label="Total mentions"
            onClick={openOwn}
            disabled={!hasEvidence}
          >
            <span className="text-2xl font-semibold tabular-nums">
              {competitor.total_mentions}
            </span>
          </Stat>
          <Stat label="Avg. rank" onClick={openOwn} disabled={!hasEvidence}>
            <span className="text-2xl font-semibold tabular-nums">
              {hasEvidence ? `#${(competitor.avg_order + 1).toFixed(1)}` : "—"}
            </span>
          </Stat>
        </div>
        <SuggestedAliasReview
          competitor={competitor}
          onReview={onReviewAlias}
          pendingAlias={pendingAlias}
        />
        <PromptAppearances
          appearances={competitor.per_prompt}
          onOpenResult={onOpenResult}
        />
        <TrendChart trend={competitor.trend} onSelectRun={onSelectRun} />
      </CardContent>
    </Card>
  )
}

function Stat({
  label,
  onClick,
  disabled,
  children,
}: {
  label: string
  onClick: () => void
  disabled?: boolean
  children: ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title="Open a response behind this number"
      className="flex flex-col items-start gap-0.5 rounded enabled:cursor-pointer enabled:hover:opacity-70 disabled:opacity-70"
    >
      <span className="text-xs text-muted-foreground">{label}</span>
      {children}
    </button>
  )
}

function VsSelf({ vsSelf }: { vsSelf: number }) {
  const rounded = Math.round(vsSelf * 10) / 10
  const sign = rounded > 0 ? "+" : ""
  return (
    <Badge
      variant={rounded > 0 ? "destructive" : "secondary"}
      className="text-sm"
    >
      {sign}
      {rounded.toFixed(1)} pts
    </Badge>
  )
}

function PromptAppearances({
  appearances,
  onOpenResult,
}: {
  appearances: CompetitorPromptAppearance[]
  onOpenResult: (ids: string[]) => void
}) {
  if (appearances.length === 0) return null
  return (
    <div className="flex flex-col gap-1.5">
      <h3 className="text-xs font-medium text-muted-foreground">Prompts</h3>
      <div className="max-h-36 overflow-auto rounded-md border">
        {appearances.map((appearance) => (
          <button
            key={appearance.prompt_id}
            type="button"
            onClick={() => onOpenResult(appearance.result_ids)}
            title="Open a response for this prompt"
            className="flex w-full items-start justify-between gap-3 border-b px-3 py-2 text-left text-xs last:border-b-0 hover:bg-muted/50"
          >
            <span className="line-clamp-2 min-w-0">
              {appearance.prompt_text}
            </span>
            <span className="shrink-0 text-muted-foreground tabular-nums">
              {appearance.result_ids.length}
            </span>
          </button>
        ))}
      </div>
    </div>
  )
}

function SuggestedAliasReview({
  competitor,
  onReview,
  pendingAlias,
}: {
  competitor: Competitor
  onReview: (
    competitorId: string,
    alias: string,
    action: "approve" | "reject"
  ) => void
  pendingAlias?: string
}) {
  if (competitor.suggested_aliases.length === 0) return null
  return (
    <div className="flex flex-col gap-1.5">
      <h3 className="text-xs font-medium text-muted-foreground">
        Suggested aliases
      </h3>
      <div className="flex flex-col gap-1.5">
        {competitor.suggested_aliases.map((alias) => {
          const pending = pendingAlias === `${competitor.id}\u0000${alias}`
          return (
            <div
              key={alias}
              className="flex flex-wrap items-center justify-between gap-2 rounded-md bg-muted/50 px-2.5 py-2"
            >
              <Badge variant="outline">{alias}</Badge>
              <div className="flex items-center gap-2">
                <Button
                  size="xs"
                  aria-label={`Approve ${alias} as an alias for ${competitor.name}`}
                  disabled={pending}
                  onClick={() => onReview(competitor.id, alias, "approve")}
                >
                  Approve
                </Button>
                <Button
                  size="xs"
                  variant="outline"
                  aria-label={`Reject ${alias} as an alias for ${competitor.name}`}
                  disabled={pending}
                  onClick={() => onReview(competitor.id, alias, "reject")}
                >
                  Reject
                </Button>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

function TrendChart({
  trend,
  onSelectRun,
}: {
  trend: CompetitorTrendPoint[]
  onSelectRun: (runID: string) => void
}) {
  if (trend.length === 0) {
    return <p className="text-xs text-muted-foreground">No runs yet.</p>
  }
  if (trend.length === 1) {
    const point = trend[0]
    return (
      <button
        type="button"
        onClick={() => onSelectRun(point.run_id)}
        className="flex cursor-pointer items-center gap-2 self-start rounded-lg border px-3 py-2 text-left text-xs hover:bg-muted/50"
      >
        <span className="size-2.5 rounded-full bg-primary" />
        <span>
          {shortDate(dateMs(point.scheduled_for))} ·{" "}
          {formatPercent(point.percent)} · the trend line starts after the next
          run
        </span>
      </button>
    )
  }

  const data = trend.map((point) => ({ x: dateMs(point.scheduled_for), point }))
  return (
    <div className="flex flex-col gap-1.5">
      <ChartContainer config={chartConfig} className="h-32 w-full">
        <LineChart
          accessibilityLayer
          data={data}
          margin={{ left: 4, right: 12, top: 8 }}
          onClick={(state) => {
            const point = (
              state as unknown as {
                activePayload?: { payload: { point: CompetitorTrendPoint } }[]
              }
            ).activePayload?.[0]?.payload.point
            if (point) onSelectRun(point.run_id)
          }}
        >
          <CartesianGrid vertical={false} />
          <XAxis
            dataKey="x"
            type="number"
            scale="time"
            domain={["dataMin", "dataMax"]}
            ticks={data.map((d) => d.x)}
            tickLine={false}
            axisLine={false}
            tickMargin={8}
            tickFormatter={(ms: number) => shortDate(ms)}
          />
          <YAxis
            domain={[0, 100]}
            width={36}
            tickLine={false}
            axisLine={false}
            tickFormatter={(v: number) => `${v}%`}
          />
          <ChartTooltip cursor content={<TrendTooltip />} />
          <Line
            dataKey="point.percent"
            name="percent"
            type="monotone"
            stroke="var(--color-percent)"
            strokeWidth={2}
            dot={{ r: 3 }}
            activeDot={{ r: 5 }}
          />
        </LineChart>
      </ChartContainer>
      <p className="text-xs text-muted-foreground">
        Click a week to see its responses.
      </p>
    </div>
  )
}

function TrendTooltip({
  active,
  payload,
}: {
  active?: boolean
  payload?: { payload: { point: CompetitorTrendPoint } }[]
}) {
  if (!active || !payload?.length) return null
  const point = payload[0].payload.point
  return (
    <div className="rounded-lg border bg-background px-3 py-2 text-xs shadow-md">
      <div className="font-medium">
        {shortDate(dateMs(point.scheduled_for))}
      </div>
      <div className="text-muted-foreground">
        {formatPercent(point.percent)} · mentioned in {point.mentioned} of{" "}
        {point.analyzed}
      </div>
    </div>
  )
}

function DismissedSection({
  competitors,
  self,
  focus,
  onOpenResult,
  onStatusChange,
  pendingCompetitorID,
  onReviewAlias,
  pendingAlias,
}: {
  competitors: Competitor[]
  self: CompetitorSelf
  focus: boolean
  onOpenResult: (ids: string[]) => void
  onStatusChange: (
    competitorId: string,
    status: "tracked" | "dismissed"
  ) => void
  pendingCompetitorID?: string
  onReviewAlias: (
    competitorId: string,
    alias: string,
    action: "approve" | "reject"
  ) => void
  pendingAlias?: string
}) {
  const [open, setOpen] = useState(focus)
  const ref = useScrollIntoView<HTMLDivElement>(focus)
  if (competitors.length === 0) return null
  return (
    <section ref={ref} className="flex flex-col gap-2">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-controls="dismissed-competitors-panel"
        className="flex cursor-pointer items-center gap-1.5 text-left"
      >
        {open ? (
          <ChevronDown className="size-4 text-muted-foreground" />
        ) : (
          <ChevronRight className="size-4 text-muted-foreground" />
        )}
        <span className="font-heading text-sm font-medium">
          Dismissed ({competitors.length})
        </span>
      </button>
      {open && (
        <div id="dismissed-competitors-panel" className="flex flex-col gap-1.5">
          <p className="text-xs text-muted-foreground">
            Dismissed competitors keep their history.
          </p>
          {competitors.map((competitor) => (
            <CoverageRow
              key={competitor.id}
              competitor={competitor}
              total={self.total_analyzed}
              onOpenResult={onOpenResult}
              aliasReview={
                <SuggestedAliasReview
                  competitor={competitor}
                  onReview={onReviewAlias}
                  pendingAlias={pendingAlias}
                />
              }
              actions={
                <Button
                  size="sm"
                  aria-label={`Track ${competitor.name} again`}
                  disabled={pendingCompetitorID === competitor.id}
                  onClick={() => onStatusChange(competitor.id, "tracked")}
                >
                  Track again
                </Button>
              }
            />
          ))}
        </div>
      )}
    </section>
  )
}

function AddCompetitorDialog({
  open,
  onOpenChange,
  submitting,
  errorMessage,
  onSubmit,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  submitting: boolean
  errorMessage?: string
  onSubmit: (input: AddCompetitorInput) => void
}) {
  const [name, setName] = useState("")
  const [aliases, setAliases] = useState("")
  const [website, setWebsite] = useState("")
  const [wasOpen, setWasOpen] = useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) {
      setName("")
      setAliases("")
      setWebsite("")
    }
  }

  const trimmedName = name.trim()
  const canSubmit = trimmedName !== "" && !submitting
  const submit = () => {
    if (!canSubmit) return
    const input: AddCompetitorInput = {
      name: trimmedName,
      aliases: aliases
        .split(",")
        .map((alias) => alias.trim())
        .filter(Boolean),
    }
    const trimmedWebsite = website.trim()
    if (trimmedWebsite !== "") input.website = trimmedWebsite
    onSubmit(input)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add competitor</DialogTitle>
          <DialogDescription>
            Manually added competitors start in your tracked list. Their history
            fills in when future responses mention them.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <label className="flex flex-col gap-1.5 text-sm font-medium">
            Name
            <Input
              value={name}
              autoFocus
              required
              placeholder="e.g. Rival Clinic"
              onChange={(event) => setName(event.currentTarget.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") submit()
              }}
            />
          </label>
          <label className="flex flex-col gap-1.5 text-sm font-medium">
            Aliases
            <Input
              value={aliases}
              placeholder="Rival Health, Rival Medical"
              onChange={(event) => setAliases(event.currentTarget.value)}
            />
            <span className="text-xs font-normal text-muted-foreground">
              Optional, separated by commas.
            </span>
          </label>
          <label className="flex flex-col gap-1.5 text-sm font-medium">
            Website
            <Input
              type="url"
              value={website}
              placeholder="https://example.com"
              onChange={(event) => setWebsite(event.currentTarget.value)}
            />
            <span className="text-xs font-normal text-muted-foreground">
              Optional.
            </span>
          </label>
        </div>

        {errorMessage && (
          <p className="text-sm text-destructive" role="alert">
            {errorMessage}
          </p>
        )}

        <DialogFooter>
          <Button
            variant="outline"
            disabled={submitting}
            onClick={() => onOpenChange(false)}
          >
            Cancel
          </Button>
          <Button disabled={!canSubmit} onClick={submit}>
            Add competitor
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function SectionHeading({
  title,
  description,
}: {
  title: string
  description: string
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <h2 className="font-heading text-sm font-medium">{title}</h2>
      <p className="text-xs text-muted-foreground">{description}</p>
    </div>
  )
}

function EmptyNote({ children }: { children: ReactNode }) {
  return (
    <p className="rounded-md border border-dashed px-3 py-4 text-sm text-muted-foreground">
      {children}
    </p>
  )
}

function useScrollIntoView<T extends HTMLElement>(focus: boolean) {
  const ref = useRef<T>(null)
  const done = useRef(false)
  useEffect(() => {
    if (focus && !done.current && ref.current) {
      done.current = true
      ref.current.scrollIntoView({ behavior: "smooth", block: "start" })
    }
  }, [focus])
  return ref
}

function statusParam(raw: string | null): CompetitorStatus | undefined {
  return raw === "discovered" || raw === "tracked" || raw === "dismissed"
    ? raw
    : undefined
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
          <Users />
        </EmptyMedia>
        <EmptyTitle>{title}</EmptyTitle>
        <EmptyDescription>{description}</EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}

function ListSkeleton() {
  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-between">
        <Skeleton className="h-7 w-40" />
        <Skeleton className="h-14 w-48" />
      </div>
      <div className="flex flex-col gap-2">
        {Array.from({ length: 4 }, (_, i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <Skeleton className="h-48 w-full" />
        <Skeleton className="h-48 w-full" />
      </div>
    </div>
  )
}

function formatPercent(value: number): string {
  return `${(Math.round(value * 10) / 10).toFixed(1)}%`
}

// scheduled_for is a plain date (YYYY-MM-DD); parse as local midnight so trend
// ticks land on the intended day (matching Overview/Responses).
function dateMs(scheduledFor: string): number {
  return new Date(`${scheduledFor}T00:00:00`).getTime()
}

function shortDate(ms: number): string {
  return new Date(ms).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  })
}
