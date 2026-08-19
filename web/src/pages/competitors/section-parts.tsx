import type { ReactNode } from "react"

import {
  AliasDecision,
  type Competitor,
} from "@/gen/opensight/v1/competitor_pb"
import { formatPercent } from "@/lib/format"
import { Alert, AlertAction, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import type {
  ActionFeedback,
  AliasReview,
  ClaimSelf,
  MutableCompetitorStatus,
} from "./shared"

// The presentational pieces shared by more than one competitor section.

export function SectionHeading({
  title,
  description,
}: {
  title: string
  description: string
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <h2 className="font-heading text-base font-medium">{title}</h2>
      <p className="text-sm text-muted-foreground">{description}</p>
    </div>
  )
}

export function EmptyNote({ children }: { children: ReactNode }) {
  return (
    <p className="rounded-lg border border-dashed px-3 py-4 text-sm text-muted-foreground">
      {children}
    </p>
  )
}

export function ActionNote({
  feedback,
  onUndo,
}: {
  feedback: ActionFeedback
  onUndo?: () => void
}) {
  return (
    <Alert
      variant={feedback.tone === "error" ? "destructive" : "default"}
      className="rounded-lg py-2"
    >
      <AlertDescription>{feedback.message}</AlertDescription>
      {onUndo && (
        <AlertAction>
          <Button size="xs" variant="outline" onClick={onUndo}>
            Undo
          </Button>
        </AlertAction>
      )}
    </Alert>
  )
}

export function VsSelf({ vsSelf }: { vsSelf: number }) {
  const rounded = Math.round(vsSelf * 10) / 10
  const sign = rounded > 0 ? "+" : ""
  return (
    <Badge
      variant={rounded > 0 ? "destructive" : "secondary"}
      className="w-fit text-sm"
    >
      {sign}
      {rounded.toFixed(1)} pts
    </Badge>
  )
}

export function CoverageRow({
  competitor,
  total,
  onOpenResult,
  actions,
  aliasReview,
  feedback,
  onUndoStatus,
}: {
  competitor: Competitor
  total: number
  onOpenResult: (ids: string[], context?: string) => void
  actions?: ReactNode
  aliasReview?: ReactNode
  feedback?: ActionFeedback
  onUndoStatus?: (status: MutableCompetitorStatus) => void
}) {
  const disabled = competitor.resultIds.length === 0
  return (
    <div className="flex min-w-0 flex-col gap-2 rounded-lg border bg-card px-3 py-3">
      <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="truncate font-medium">{competitor.name}</span>
          <button
            type="button"
            disabled={disabled}
            onClick={() =>
              onOpenResult(
                competitor.resultIds,
                `Responses mentioning ${competitor.name}`
              )
            }
            title="View responses behind this coverage"
            className="flex min-h-9 items-center gap-3 self-start rounded text-left text-sm text-muted-foreground enabled:cursor-pointer enabled:hover:text-foreground disabled:opacity-70"
          >
            <span className="tabular-nums">
              {competitor.mentioned} of {total} responses
            </span>
            <Badge variant="outline" className="tabular-nums">
              {formatPercent(competitor.mentionPercent)}
            </Badge>
          </button>
        </div>
        {actions && (
          <div className="flex shrink-0 items-center gap-2">{actions}</div>
        )}
      </div>
      {feedback && (
        <ActionNote
          feedback={feedback}
          onUndo={
            feedback.undoStatus && onUndoStatus
              ? () => onUndoStatus(feedback.undoStatus!)
              : undefined
          }
        />
      )}
      {aliasReview}
    </div>
  )
}

export function ClaimSelfButton({
  competitor,
  onClaimSelf,
  disabled,
}: {
  competitor: Competitor
  onClaimSelf: ClaimSelf
  disabled?: boolean
}) {
  return (
    <Button
      size="sm"
      variant="ghost"
      className="min-h-10 sm:min-h-0"
      aria-label={`${competitor.name} is my business, not a competitor`}
      disabled={disabled}
      onClick={() => onClaimSelf(competitor)}
    >
      This is my business
    </Button>
  )
}

export function SuggestedAliasReview({
  competitor,
  onReview,
  pendingAlias,
}: {
  competitor: Competitor
  onReview: AliasReview
  pendingAlias?: string
}) {
  if (competitor.suggestedAliases.length === 0) return null
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <h3 className="text-sm font-medium">Suggested aliases</h3>
      <div className="flex flex-col gap-2">
        {competitor.suggestedAliases.map((alias) => {
          const pending = pendingAlias === `${competitor.id}\u0000${alias}`
          return (
            <div
              key={alias}
              className="flex min-w-0 flex-col gap-2 rounded-lg bg-muted/50 px-3 py-2 sm:flex-row sm:items-center sm:justify-between"
            >
              <Badge
                variant="outline"
                className="max-w-full self-start truncate"
              >
                {alias}
              </Badge>
              <div className="flex items-center gap-2">
                <Button
                  size="sm"
                  className="min-h-10 flex-1 sm:min-h-0 sm:flex-none"
                  aria-label={`Approve ${alias} as an alias for ${competitor.name}`}
                  disabled={pending}
                  onClick={() =>
                    onReview(competitor, alias, AliasDecision.APPROVE)
                  }
                >
                  Approve
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  className="min-h-10 flex-1 sm:min-h-0 sm:flex-none"
                  aria-label={`Reject ${alias} as an alias for ${competitor.name}`}
                  disabled={pending}
                  onClick={() =>
                    onReview(competitor, alias, AliasDecision.REJECT)
                  }
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
