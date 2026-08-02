import { ArrowDownRight, ArrowUpRight } from "lucide-react"

import type { PromptSummary } from "@/gen/opensight/v1/prompt_pb"
import { pluralize } from "@/lib/format"
import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { PanelEmpty, QuestionRow } from "./panels"

type PromptChangeState = "gained" | "lost"

interface LatestPromptChange {
  prompt: PromptSummary
  state: PromptChangeState
  resultId: string
}

export function WhatChanged({
  prompts,
  onOpenResult,
}: {
  prompts: PromptSummary[]
  onOpenResult: (id: string, context: string) => void
}) {
  const changes = latestPromptChanges(prompts)
  const withoutBaseline = prompts.filter(
    (prompt) => prompt.trend.length < 2
  ).length
  const lost = changes.filter((change) => change.state === "lost")
  const gained = changes.filter((change) => change.state === "gained")

  return (
    <section aria-labelledby="what-changed-title">
      <Card>
        <CardHeader>
          <CardTitle>
            <h2 id="what-changed-title">What changed</h2>
          </CardTitle>
          <CardDescription>
            Each question compares its two latest analyzed responses.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {changes.length === 0 ? (
            <PanelEmpty>
              No questions gained or lost visibility in the latest comparison.
            </PanelEmpty>
          ) : (
            <div className="grid gap-6 md:grid-cols-2">
              <ChangeGroup
                title="No longer visible"
                items={lost}
                badgeVariant="destructive"
                icon={<ArrowDownRight data-icon="inline-start" />}
                empty="No questions lost visibility."
                onOpenResult={onOpenResult}
              />
              <ChangeGroup
                title="Now visible"
                items={gained}
                badgeVariant="secondary"
                icon={<ArrowUpRight data-icon="inline-start" />}
                empty="No questions gained visibility."
                onOpenResult={onOpenResult}
              />
            </div>
          )}
        </CardContent>
        {withoutBaseline > 0 && (
          <CardFooter className="border-t text-sm text-muted-foreground">
            {withoutBaseline} {pluralize(withoutBaseline, "question")}{" "}
            {withoutBaseline === 1 ? "needs" : "need"} another analyzed response
            before a change can be measured.
          </CardFooter>
        )}
      </Card>
    </section>
  )
}

function ChangeGroup({
  title,
  items,
  badgeVariant,
  icon,
  empty,
  onOpenResult,
}: {
  title: string
  items: LatestPromptChange[]
  badgeVariant: "destructive" | "secondary"
  icon: React.ReactNode
  empty: string
  onOpenResult: (id: string, context: string) => void
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-sm font-medium">{title}</h3>
        <Badge variant={badgeVariant} className="tabular-nums">
          {icon}
          {items.length}
        </Badge>
      </div>
      {items.length === 0 ? (
        <PanelEmpty>{empty}</PanelEmpty>
      ) : (
        <div className="flex flex-col gap-0.5">
          {items.map((item) => (
            <QuestionRow
              key={item.prompt.id}
              prompt={item.prompt}
              onClick={() =>
                onOpenResult(
                  item.resultId,
                  `Latest response for “${item.prompt.text}”`
                )
              }
            />
          ))}
        </div>
      )}
    </div>
  )
}

function latestPromptChanges(prompts: PromptSummary[]): LatestPromptChange[] {
  return prompts.flatMap((prompt) => {
    if (prompt.trend.length < 2) return []
    const current = prompt.trend[prompt.trend.length - 1]
    const previous = prompt.trend[prompt.trend.length - 2]
    let state: PromptChangeState | undefined
    if (!previous.mentioned && current.mentioned) state = "gained"
    if (previous.mentioned && !current.mentioned) state = "lost"
    if (!state) return []
    return [
      {
        prompt,
        state,
        resultId: current.resultId,
      },
    ]
  })
}
