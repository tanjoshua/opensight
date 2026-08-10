// Prompt detail (INS-2, design 06): a single prompt's full result history plus
// its replacement lineage. Reached by drilling in from the Prompts table or by
// walking a lineage link — GetPrompt reaches retired prompts too, so a retired
// predecessor renders here correctly. Every result row opens the Response drawer.
import {
  createConnectQueryKey,
  skipToken,
  useMutation,
  useQuery,
} from "@connectrpc/connect-query"
import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt"
import { useQueryClient } from "@tanstack/react-query"
import { ArrowLeft, MessageSquareText, Replace } from "lucide-react"
import { useState } from "react"
import { Link, useParams } from "react-router"

import { useAccountNavigate, useAccountPath } from "@/lib/account-path"

import { errorMessage } from "@/api/errors"
import { useCurrentBusiness } from "@/api/hooks"
import { promptStatusLabel, resultStatusLabel } from "@/api/labels"
import { PromptStatus, ResultStatus } from "@/gen/opensight/v1/common_pb"
import type { Prompt } from "@/gen/opensight/v1/prompt_pb"
import type { PromptResult } from "@/gen/opensight/v1/result_pb"
import {
  getPrompt,
  listPrompts,
  replacePrompt,
} from "@/gen/opensight/v1/prompt-PromptService_connectquery"
import { PromptConfirmDialog } from "@/components/prompt-confirm-dialog"
import { ResponseDrawer } from "@/components/response-drawer"
import { SectionMessage } from "@/components/section-message"
import {
  evidenceSelection,
  type EvidenceSelection,
} from "@/components/evidence-selection"
import { formatDateOnly } from "@/lib/format"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
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
  const navigate = useAccountNavigate()
  const path = useAccountPath()
  const { businessId } = useCurrentBusiness()
  const queryClient = useQueryClient()
  const promptQuery = useQuery(
    getPrompt,
    id === undefined ? skipToken : { promptId: id }
  )
  const [selectedEvidence, setSelectedEvidence] = useState<EvidenceSelection>()
  const [replaceOpen, setReplaceOpen] = useState(false)
  const replacePromptMutation = useMutation(replacePrompt, {
    onSuccess: (_data, variables) => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: listPrompts,
          input: businessId === undefined ? undefined : { businessId },
          cardinality: "finite",
        }),
      })
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: getPrompt,
          input: { promptId: variables.promptId },
          cardinality: "finite",
        }),
      })
    },
  })

  if (promptQuery.isError) {
    return (
      <SectionMessage
        icon={MessageSquareText}
        title="Prompt not found"
        description="This prompt could not be loaded. It may have been removed."
      />
    )
  }
  if (!promptQuery.data) {
    return <DetailSkeleton />
  }
  const { prompt, lineage, results } = promptQuery.data
  if (!prompt) {
    return (
      <SectionMessage
        icon={MessageSquareText}
        title="Prompt not found"
        description="This prompt could not be loaded. It may have been removed."
      />
    )
  }
  const replacements = buildReplacements(prompt, lineage)

  const replaceError = replacePromptMutation.isError
    ? errorMessage(
        replacePromptMutation.error,
        "Could not replace prompt. Try again."
      )
    : undefined

  const openReplace = (open: boolean) => {
    setReplaceOpen(open)
    if (!open) replacePromptMutation.reset()
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
          variant={
            prompt.status === PromptStatus.RETIRED ? "outline" : "secondary"
          }
          className="capitalize"
        >
          {promptStatusLabel(prompt.status)}
        </Badge>
        {prompt.status === PromptStatus.ACTIVE && (
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
        submitting={replacePromptMutation.isPending}
        errorMessage={replaceError}
        onSubmit={(text) =>
          replacePromptMutation.mutate(
            { promptId: prompt.id, text, confirmed: true },
            {
              onSuccess: (data) => {
                setReplaceOpen(false)
                if (data.prompt) navigate(`/prompts/${data.prompt.id}`)
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
                  to={path(`/prompts/${r.replacedId}`)}
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
                    onOpen={() =>
                      setSelectedEvidence(
                        evidenceSelection(
                          [result.id],
                          "Response history for this question"
                        )
                      )
                    }
                  />
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      <ResponseDrawer
        evidence={selectedEvidence}
        onOpenChange={(open) => {
          if (!open) setSelectedEvidence(undefined)
        }}
      />
    </div>
  )
}

function ResultRow({
  result,
  onOpen,
}: {
  result: PromptResult
  onOpen: () => void
}) {
  return (
    <TableRow className="cursor-pointer" onClick={onOpen}>
      <TableCell className="whitespace-nowrap">
        {formatDate(result.requestedAt)}
      </TableCell>
      <TableCell className="max-w-0">
        {result.status === ResultStatus.FAILED ? (
          <span className="line-clamp-2 whitespace-normal text-destructive">
            {result.error ?? "Unknown error"}
          </span>
        ) : (
          <span className="line-clamp-2 whitespace-normal text-muted-foreground">
            {result.responseText}
          </span>
        )}
      </TableCell>
      <TableCell className="space-x-1.5 whitespace-nowrap">
        <Badge
          variant={
            result.status === ResultStatus.FAILED ? "destructive" : "secondary"
          }
        >
          {resultStatusLabel(result.status)}
        </Badge>
        {result.unanalyzed && <Badge variant="outline">not yet analyzed</Badge>}
      </TableCell>
    </TableRow>
  )
}

interface Replacement {
  replacedId: string
  replacedText: string
  on: Timestamp | undefined
}

// Walk the newest-first chain [prompt, ...lineage]: a node with a
// replaces_prompt_id replaced the next node in the chain, on that node's
// created_at. The API only exposes the backward chain (replaces_prompt_id), so
// this renders "replaced X" going back — there is no forward "replaced by Y" link.
function buildReplacements(prompt: Prompt, lineage: Prompt[]): Replacement[] {
  const chain = [prompt, ...lineage]
  const out: Replacement[] = []
  for (let i = 0; i < chain.length; i++) {
    const node = chain[i]
    const predecessor = chain[i + 1]
    if (node.replacesPromptId && predecessor) {
      out.push({
        replacedId: predecessor.id,
        replacedText: predecessor.text,
        on: node.createdAt,
      })
    }
  }
  return out
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

function formatDate(value: Timestamp | undefined): string {
  if (value === undefined) return "-"
  const date = timestampDate(value)
  if (Number.isNaN(date.valueOf())) return "-"
  return formatDateOnly(date)
}
