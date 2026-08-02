import { ArrowRight } from "lucide-react"

import type { PromptSummary } from "@/gen/opensight/v1/prompt_pb"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Empty, EmptyDescription, EmptyHeader } from "@/components/ui/empty"

// The compact list cards of the Brief and the rows they hold. Every row is a
// door: clicking one opens the responses behind it.
export function Panel({
  title,
  description,
  footer,
  children,
}: {
  title: string
  description: string
  footer?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <Card className="h-full">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col gap-0.5">
        {children}
      </CardContent>
      {footer && <CardFooter className="border-t">{footer}</CardFooter>}
    </Card>
  )
}

export function StatRow({
  label,
  value,
  disabled,
  onClick,
}: {
  label: string
  value: string
  disabled?: boolean
  onClick: () => void
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      disabled={disabled}
      onClick={onClick}
      className="h-auto min-h-11 w-full justify-between gap-2 px-2 py-2 text-left whitespace-normal"
    >
      <span className="truncate">{label}</span>
      <span className="shrink-0 text-muted-foreground tabular-nums">
        {value}
      </span>
    </Button>
  )
}

export function QuestionRow({
  prompt,
  onClick,
}: {
  prompt: PromptSummary
  onClick: () => void
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      onClick={onClick}
      className="h-auto min-h-11 w-full justify-between gap-3 px-2 py-2 text-left whitespace-normal"
    >
      <span className="line-clamp-2">{prompt.text}</span>
      <ArrowRight data-icon="inline-end" />
    </Button>
  )
}

export function PanelEmpty({ children }: { children: React.ReactNode }) {
  return (
    <Empty className="min-h-24 p-4">
      <EmptyHeader>
        <EmptyDescription>{children}</EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}
