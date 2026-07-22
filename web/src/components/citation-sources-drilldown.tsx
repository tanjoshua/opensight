import { Link2 } from "lucide-react"

import {
  useCitationSources,
  type CitationPage,
  type CitationPrompt,
  type CitationSource,
  type CitationSubjectStat,
} from "@/api/citations"
import { Badge } from "@/components/ui/badge"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"

export function CitationSourcesDrilldown({
  businessId,
  domain,
  open,
  onOpenChange,
  onOpenResult,
}: {
  businessId: string | undefined
  domain: string | undefined
  open: boolean
  onOpenChange: (open: boolean) => void
  onOpenResult: (ids: string[]) => void
}) {
  const sources = useCitationSources(open ? businessId : undefined, { domain })
  const source = sources.data?.domains[0]

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-3xl">
        <SheetHeader>
          <SheetTitle>{source?.domain ?? domain ?? "Citation source"}</SheetTitle>
          <SheetDescription>
            {source
              ? `${source.frequency} ${plural(source.frequency, "response")}`
              : "Citation source detail"}
          </SheetDescription>
        </SheetHeader>

        <div className="flex flex-col gap-5 px-6 pb-6">
          {sources.isLoading && <DrilldownSkeleton />}
          {sources.isError && (
            <SectionMessage
              title="Something went wrong"
              description="The citation source could not be loaded."
            />
          )}
          {sources.data && !source && (
            <SectionMessage
              title="No citations"
              description="This source is not present in analyzed responses."
            />
          )}
          {source && (
            <>
              <SubjectSplit source={source} onOpenResult={onOpenResult} />
              <PagesList pages={source.pages} onOpenResult={onOpenResult} />
              <PromptsList
                prompts={source.prompts}
                onOpenResult={onOpenResult}
              />
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

function SubjectSplit({
  source,
  onOpenResult,
}: {
  source: CitationSource
  onOpenResult: (ids: string[]) => void
}) {
  const items = [
    ["Business", source.subjects.business],
    ["Competitor", source.subjects.competitor],
    ["Other", source.subjects.other],
    ["Unknown", source.subjects.unknown],
  ] as const
  return (
    <section className="flex flex-col gap-2">
      <h2 className="font-heading text-sm font-medium">Subject split</h2>
      <div className="flex flex-wrap gap-2">
        {items.map(([label, stat]) => (
          <SubjectBadge
            key={label}
            label={label}
            stat={stat}
            onOpenResult={onOpenResult}
          />
        ))}
      </div>
    </section>
  )
}

function SubjectBadge({
  label,
  stat,
  onOpenResult,
}: {
  label: string
  stat: CitationSubjectStat
  onOpenResult: (ids: string[]) => void
}) {
  return (
    <button
      type="button"
      disabled={stat.frequency === 0}
      title="Open a response behind this subject"
      onClick={() => onOpenResult(stat.result_ids)}
      className="enabled:cursor-pointer enabled:hover:opacity-75 disabled:opacity-60"
    >
      <Badge variant="outline" className="gap-1">
        <span>{label}</span>
        <span className="tabular-nums">{stat.frequency}</span>
      </Badge>
    </button>
  )
}

function PagesList({
  pages,
  onOpenResult,
}: {
  pages: CitationPage[]
  onOpenResult: (ids: string[]) => void
}) {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="font-heading text-sm font-medium">Pages</h2>
      {pages.length === 0 ? (
        <p className="text-sm text-muted-foreground">No cited pages.</p>
      ) : (
        <div className="overflow-hidden rounded-lg border">
          {pages.map((page) => (
            <button
              key={page.url}
              type="button"
              onClick={() => onOpenResult(page.result_ids)}
              title="Open a response citing this page"
              className="flex w-full flex-col gap-2 border-b px-3 py-3 text-left last:border-b-0 hover:bg-muted/50"
            >
              <div className="flex min-w-0 items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="line-clamp-1 text-sm font-medium">
                    {page.title ?? page.url}
                  </div>
                  <div className="line-clamp-1 text-xs text-muted-foreground">
                    {page.url}
                  </div>
                </div>
                <Badge variant="secondary" className="shrink-0 tabular-nums">
                  {page.frequency}
                </Badge>
              </div>
              <SubjectMiniSplit
                business={page.subjects.business.frequency}
                competitor={page.subjects.competitor.frequency}
                other={page.subjects.other.frequency}
                unknown={page.subjects.unknown.frequency}
              />
            </button>
          ))}
        </div>
      )}
    </section>
  )
}

function PromptsList({
  prompts,
  onOpenResult,
}: {
  prompts: CitationPrompt[]
  onOpenResult: (ids: string[]) => void
}) {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="font-heading text-sm font-medium">Prompts</h2>
      {prompts.length === 0 ? (
        <p className="text-sm text-muted-foreground">No associated prompts.</p>
      ) : (
        <div className="overflow-hidden rounded-lg border">
          {prompts.map((prompt) => (
            <button
              key={prompt.prompt_id}
              type="button"
              onClick={() => onOpenResult(prompt.result_ids)}
              title="Open a response for this prompt"
              className="flex w-full items-start justify-between gap-3 border-b px-3 py-3 text-left last:border-b-0 hover:bg-muted/50"
            >
              <span className="line-clamp-2 min-w-0 text-sm">
                {prompt.prompt_text}
              </span>
              <Badge variant="secondary" className="shrink-0 tabular-nums">
                {prompt.frequency}
              </Badge>
            </button>
          ))}
        </div>
      )}
    </section>
  )
}

function SubjectMiniSplit({
  business,
  competitor,
  other,
  unknown,
}: {
  business: number
  competitor: number
  other: number
  unknown: number
}) {
  const items = [
    ["business", business],
    ["competitor", competitor],
    ["other", other],
    ["unknown", unknown],
  ] as const
  return (
    <div className="flex flex-wrap gap-1.5">
      {items
        .filter(([, value]) => value > 0)
        .map(([label, value]) => (
          <Badge key={label} variant="outline" className="text-[11px]">
            {label} {value}
          </Badge>
        ))}
    </div>
  )
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
          <Link2 />
        </EmptyMedia>
        <EmptyTitle>{title}</EmptyTitle>
        <EmptyDescription>{description}</EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}

function DrilldownSkeleton() {
  return (
    <div className="flex flex-col gap-4">
      <Skeleton className="h-8 w-64" />
      <Skeleton className="h-24 w-full" />
      <Skeleton className="h-36 w-full" />
      <Skeleton className="h-28 w-full" />
    </div>
  )
}

function plural(count: number, noun: string): string {
  return count === 1 ? noun : `${noun}s`
}
