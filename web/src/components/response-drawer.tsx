import { type ReactNode, useState } from "react"
import { FileJson } from "lucide-react"

import { useResult } from "@/api/responses"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"

export function ResponseDrawer({
  resultId,
  onOpenChange,
}: {
  resultId: string | undefined
  onOpenChange: (open: boolean) => void
}) {
  const [includeRaw, setIncludeRaw] = useState(false)
  const detail = useResult(resultId, { includeRaw })

  return (
    <Sheet
      open={resultId !== undefined}
      onOpenChange={(open) => {
        if (!open) setIncludeRaw(false)
        onOpenChange(open)
      }}
    >
      <SheetContent className="w-full overflow-y-auto sm:max-w-xl">
        <SheetHeader>
          <SheetTitle>Response detail</SheetTitle>
          <SheetDescription>
            Stored answer, prompt, model, and run metadata.
          </SheetDescription>
        </SheetHeader>

        <div className="flex flex-col gap-5 px-6 pb-6">
          {detail.isLoading && <DrawerSkeleton />}
          {detail.isError && (
            <p className="text-sm text-destructive" role="alert">
              The response could not be loaded. Try again.
            </p>
          )}
          {detail.data && (
            <>
              <DetailSection title="Prompt">
                <p className="text-sm whitespace-pre-wrap">
                  {detail.data.prompt?.text ?? detail.data.prompt_id}
                </p>
              </DetailSection>

              <DetailSection
                title={detail.data.status === "failed" ? "Error" : "Answer"}
              >
                <p
                  className={
                    detail.data.status === "failed"
                      ? "text-sm whitespace-pre-wrap text-destructive"
                      : "text-sm leading-6 whitespace-pre-wrap"
                  }
                >
                  {detail.data.status === "failed"
                    ? (detail.data.error ?? "Unknown error")
                    : (detail.data.response_text ?? "")}
                </p>
              </DetailSection>

              <DetailSection title="Run">
                <dl className="grid grid-cols-[7rem_1fr] gap-x-3 gap-y-2 text-sm">
                  <dt className="text-muted-foreground">Status</dt>
                  <dd>{detail.data.status}</dd>
                  <dt className="text-muted-foreground">Model</dt>
                  <dd>{detail.data.model ?? "Not recorded"}</dd>
                  <dt className="text-muted-foreground">Requested</dt>
                  <dd>{formatDateTime(detail.data.requested_at)}</dd>
                  <dt className="text-muted-foreground">Completed</dt>
                  <dd>{formatDateTime(detail.data.completed_at)}</dd>
                  {detail.data.run && (
                    <>
                      <dt className="text-muted-foreground">Scheduled</dt>
                      <dd>{formatRunDate(detail.data.run.scheduled_for)}</dd>
                      <dt className="text-muted-foreground">Run status</dt>
                      <dd>{detail.data.run.status}</dd>
                    </>
                  )}
                </dl>
              </DetailSection>

              <DetailSection title="Request">
                <JSONBlock
                  value={detail.data.request}
                  empty="No request params stored."
                />
              </DetailSection>

              <Separator />

              <div className="flex flex-col gap-3">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="w-fit"
                  onClick={() => setIncludeRaw((value) => !value)}
                >
                  <FileJson data-icon="inline-start" />
                  {includeRaw ? "Hide raw JSON" : "Show raw JSON"}
                </Button>
                {includeRaw && (
                  <JSONBlock
                    value={detail.data.raw_response}
                    empty={
                      detail.isFetching
                        ? "Loading raw JSON."
                        : "No raw response stored."
                    }
                  />
                )}
              </div>
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

function DetailSection({
  title,
  children,
}: {
  title: string
  children: ReactNode
}) {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="font-heading text-sm font-medium">{title}</h2>
      {children}
    </section>
  )
}

function JSONBlock({ value, empty }: { value: unknown; empty: string }) {
  if (value === undefined || value === null || value === "") {
    return <p className="text-sm text-muted-foreground">{empty}</p>
  }
  return (
    <pre className="max-h-80 overflow-auto rounded-lg bg-muted p-3 text-xs leading-5">
      {JSON.stringify(value, null, 2)}
    </pre>
  )
}

function DrawerSkeleton() {
  return (
    <div className="flex flex-col gap-4">
      <Skeleton className="h-4 w-24" />
      <Skeleton className="h-20 w-full" />
      <Skeleton className="h-4 w-20" />
      <Skeleton className="h-32 w-full" />
      <Skeleton className="h-24 w-full" />
    </div>
  )
}

function formatDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.valueOf())) return value
  return date.toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  })
}

function formatRunDate(value: string): string {
  const date = new Date(`${value}T00:00:00`)
  if (Number.isNaN(date.valueOf())) return value
  return date.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  })
}
