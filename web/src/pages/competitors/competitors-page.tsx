import { ChevronDown, ChevronRight, Users } from "lucide-react"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { useNavigate, useSearchParams } from "react-router"
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts"

import { useMe } from "@/api/auth"
import {
  useAllCompetitors,
  type Competitor,
  type CompetitorPromptAppearance,
  type CompetitorSelf,
  type CompetitorStatus,
  type CompetitorTrendPoint,
} from "@/api/competitors"
import { ResponseDrawer } from "@/components/response-drawer"
import { Badge } from "@/components/ui/badge"
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
  const [selectedResultID, setSelectedResultID] = useState<string>()
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
  if (competitors.length === 0) {
    return (
      <SectionMessage
        title="No competitors yet"
        description="Competitors appear here once analysis starts naming other clinics in your responses."
      />
    )
  }

  const discovered = competitors.filter((c) => c.status === "discovered")
  const tracked = competitors.filter((c) => c.status === "tracked")
  const dismissed = competitors.filter((c) => c.status === "dismissed")

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="font-heading text-lg font-semibold">Competitors</h1>
        <SelfBaseline self={self} onOpenResult={openResult} />
      </div>

      <DiscoveredSection
        competitors={discovered}
        self={self}
        focus={focus === "discovered"}
        onOpenResult={openResult}
      />
      <TrackedSection
        competitors={tracked}
        focus={focus === "tracked"}
        onOpenResult={openResult}
        onSelectRun={(runID) => navigate(`/responses?run=${runID}`)}
      />
      <DismissedSection
        competitors={dismissed}
        self={self}
        focus={focus === "dismissed"}
        onOpenResult={openResult}
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
}: {
  competitors: Competitor[]
  self: CompetitorSelf
  focus: boolean
  onOpenResult: (ids: string[]) => void
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
}: {
  competitor: Competitor
  total: number
  onOpenResult: (ids: string[]) => void
}) {
  const disabled = competitor.result_ids.length === 0
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={() => onOpenResult(competitor.result_ids)}
      title="Open a response behind this number"
      className="flex w-full flex-col gap-2 rounded-md border px-3 py-2 text-left enabled:cursor-pointer enabled:hover:bg-muted/50 disabled:opacity-70 sm:flex-row sm:items-center sm:justify-between"
    >
      <span className="min-w-0 truncate font-medium">{competitor.name}</span>
      <span className="flex shrink-0 flex-wrap items-center gap-3 text-sm text-muted-foreground">
        <span className="tabular-nums">
          in {competitor.mentioned} of {total} responses
        </span>
        <Badge variant="outline" className="tabular-nums">
          {formatPercent(competitor.mention_percent)}
        </Badge>
      </span>
    </button>
  )
}

function TrackedSection({
  competitors,
  focus,
  onOpenResult,
  onSelectRun,
}: {
  competitors: Competitor[]
  focus: boolean
  onOpenResult: (ids: string[]) => void
  onSelectRun: (runID: string) => void
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
}: {
  competitor: Competitor
  onOpenResult: (ids: string[]) => void
  onSelectRun: (runID: string) => void
}) {
  const openOwn = () => onOpenResult(competitor.result_ids)
  return (
    <Card className="gap-3">
      <CardHeader>
        <CardTitle className="text-base">{competitor.name}</CardTitle>
        <CardDescription>
          Mentioned in {competitor.mentioned} responses
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
          <Stat label="Mention %" onClick={openOwn}>
            <span className="text-2xl font-semibold tabular-nums">
              {formatPercent(competitor.mention_percent)}
            </span>
          </Stat>
          <Stat label="vs you" onClick={openOwn}>
            <VsSelf vsSelf={competitor.vs_self} />
          </Stat>
          <Stat label="Total mentions" onClick={openOwn}>
            <span className="text-2xl font-semibold tabular-nums">
              {competitor.total_mentions}
            </span>
          </Stat>
          <Stat label="Avg. rank" onClick={openOwn}>
            <span className="text-2xl font-semibold tabular-nums">
              #{(competitor.avg_order + 1).toFixed(1)}
            </span>
          </Stat>
        </div>
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
  children,
}: {
  label: string
  onClick: () => void
  children: ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title="Open a response behind this number"
      className="flex cursor-pointer flex-col items-start gap-0.5 rounded hover:opacity-70"
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
}: {
  competitors: Competitor[]
  self: CompetitorSelf
  focus: boolean
  onOpenResult: (ids: string[]) => void
}) {
  const [open, setOpen] = useState(focus)
  const ref = useScrollIntoView<HTMLDivElement>(focus)
  if (competitors.length === 0) return null
  return (
    <section ref={ref} className="flex flex-col gap-2">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
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
        <div className="flex flex-col gap-1.5">
          <p className="text-xs text-muted-foreground">
            Dismissed competitors keep their history.
          </p>
          {competitors.map((competitor) => (
            <CoverageRow
              key={competitor.id}
              competitor={competitor}
              total={self.total_analyzed}
              onOpenResult={onOpenResult}
            />
          ))}
        </div>
      )}
    </section>
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
