// Prompt detail (INS-2, design 06): a single prompt's full result history plus
// its replacement lineage. Reached by drilling in from the Prompts table or by
// walking a lineage link — GetPrompt reaches retired prompts too, so a retired
// predecessor renders here correctly. Every result row opens the Response drawer.
import { ArrowLeft, MessageSquareText, Replace } from "lucide-react"
import { useState } from "react"
import { Link, useNavigate, useParams } from "react-router"

import { useMe } from "@/api/auth"
import { ApiError } from "@/api/client"
import {
  usePrompt,
  useReplacePrompt,
  type PromptDetailResult,
  type PromptLineageNode,
} from "@/api/prompts"
import { PromptConfirmDialog } from "@/components/prompt-confirm-dialog"
import { ResponseDrawer } from "@/components/response-drawer"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

export function PromptDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const me = useMe()
  const businessId = me.data?.businesses[0]?.id
  const promptQuery = usePrompt(id)
  const [selectedResultID, setSelectedResultID] = useState<string>()
  const [replaceOpen, setReplaceOpen] = useState(false)
  const replacePrompt = useReplacePrompt(businessId)

  if (promptQuery.isError) {
    return (
      <SectionMessage
        title="Prompt not found"
        description="This prompt could not be loaded. It may have been removed."
      />
    )
  }
  if (!promptQuery.data) {
    return <DetailSkeleton />
  }

  const { prompt, lineage, results } = promptQuery.data
  const replacements = buildReplacements(prompt, lineage)

  const replaceError =
    replacePrompt.error instanceof ApiError
      ? replacePrompt.error.message
      : replacePrompt.isError
        ? "Could not replace prompt. Try again."
        : undefined

  const openReplace = (open: boolean) => {
    setReplaceOpen(open)
    if (!open) replacePrompt.reset()
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-2">
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Back to prompts"
          onClick={() => navigate("/prompts")}
        >
          <ArrowLeft />
        </Button>
        <h1 className="font-heading text-lg font-semibold">Prompt</h1>
        <Badge
          variant={prompt.status === "retired" ? "outline" : "secondary"}
          className="capitalize"
        >
          {prompt.status}
        </Badge>
        {prompt.status === "active" && (
          <Button
            variant="outline"
            className="ms-auto"
            onClick={() => openReplace(true)}
          >
            <Replace data-icon="inline-start" />
            Replace
          </Button>
        )}
      </div>

      <PromptConfirmDialog
        mode="replace"
        open={replaceOpen}
        onOpenChange={openReplace}
        initialText={prompt.text}
        submitting={replacePrompt.isPending}
        errorMessage={replaceError}
        onSubmit={(text) =>
          replacePrompt.mutate(
            { promptId: prompt.id, text },
            {
              onSuccess: (data) => {
                setReplaceOpen(false)
                navigate(`/prompts/${data.prompt.id}`)
              },
            }
          )
        }
      />

      <Card>
        <CardHeader>
          <CardTitle className="text-base leading-6 font-normal">
            {prompt.text}
          </CardTitle>
        </CardHeader>
        {replacements.length > 0 && (
          <CardContent className="flex flex-col gap-1.5 border-t pt-4 text-sm text-muted-foreground">
            {replacements.map((r) => (
              <div key={r.replacedId}>
                Replaced{" "}
                <Link
                  to={`/prompts/${r.replacedId}`}
                  className="text-foreground underline underline-offset-2"
                >
                  “{r.replacedText}”
                </Link>{" "}
                on {formatDate(r.on)}
              </div>
            ))}
          </CardContent>
        )}
      </Card>

      <div className="flex flex-col gap-2">
        <h2 className="font-heading text-sm font-medium">Result history</h2>
        <div className="overflow-x-auto rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-40">Collected</TableHead>
                <TableHead>Response</TableHead>
                <TableHead className="w-40">Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {results.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={3}
                    className="h-24 text-center text-muted-foreground"
                  >
                    No results yet for this prompt.
                  </TableCell>
                </TableRow>
              ) : (
                results.map((result) => (
                  <ResultRow
                    key={result.id}
                    result={result}
                    onOpen={() => setSelectedResultID(result.id)}
                  />
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      <ResponseDrawer
        resultId={selectedResultID}
        onOpenChange={(open) => {
          if (!open) setSelectedResultID(undefined)
        }}
      />
    </div>
  )
}

function ResultRow({
  result,
  onOpen,
}: {
  result: PromptDetailResult
  onOpen: () => void
}) {
  return (
    <TableRow className="cursor-pointer" onClick={onOpen}>
      <TableCell className="whitespace-nowrap">
        {formatDate(result.requested_at)}
      </TableCell>
      <TableCell className="max-w-0">
        {result.status === "failed" ? (
          <span className="line-clamp-2 whitespace-normal text-destructive">
            {result.error ?? "Unknown error"}
          </span>
        ) : (
          <span className="line-clamp-2 whitespace-normal text-muted-foreground">
            {result.response_text}
          </span>
        )}
      </TableCell>
      <TableCell className="space-x-1.5 whitespace-nowrap">
        <Badge
          variant={result.status === "failed" ? "destructive" : "secondary"}
        >
          {result.status}
        </Badge>
        {result.unanalyzed && <Badge variant="outline">not yet analyzed</Badge>}
      </TableCell>
    </TableRow>
  )
}

interface Replacement {
  replacedId: string
  replacedText: string
  on: string
}

// Walk the newest-first chain [prompt, ...lineage]: a node with a
// replaces_prompt_id replaced the next node in the chain, on that node's
// created_at. The API only exposes the backward chain (replaces_prompt_id), so
// this renders "replaced X" going back — there is no forward "replaced by Y" link.
function buildReplacements(
  prompt: PromptLineageNode,
  lineage: PromptLineageNode[]
): Replacement[] {
  const chain = [prompt, ...lineage]
  const out: Replacement[] = []
  for (let i = 0; i < chain.length; i++) {
    const node = chain[i]
    const predecessor = chain[i + 1]
    if (node.replaces_prompt_id && predecessor) {
      out.push({
        replacedId: predecessor.id,
        replacedText: predecessor.text,
        on: node.created_at,
      })
    }
  }
  return out
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
          <MessageSquareText />
        </EmptyMedia>
        <EmptyTitle>{title}</EmptyTitle>
        <EmptyDescription>{description}</EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}

function DetailSkeleton() {
  return (
    <div className="flex flex-col gap-4">
      <Skeleton className="h-7 w-40" />
      <Skeleton className="h-24 w-full" />
      <div className="flex flex-col gap-2">
        {Array.from({ length: 4 }, (_, i) => (
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </div>
    </div>
  )
}

// created_at is a full timestamp; scheduled_for is a plain date (YYYY-MM-DD)
// parsed as local midnight. Both render as a short local date.
function formatDate(value: string): string {
  const iso = /^\d{4}-\d{2}-\d{2}$/.test(value) ? `${value}T00:00:00` : value
  const date = new Date(iso)
  if (Number.isNaN(date.valueOf())) return value
  return date.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  })
}
