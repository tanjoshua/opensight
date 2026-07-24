// Shared add/replace prompt dialog (POL-1, design 06). One component, two modes:
// "add" creates a new active prompt; "replace" retires the current prompt and
// starts a new one. The replace mode carries the unskippable warning — its exact
// wording is enforced here and independently on the server (confirmed: true).
import { useState } from "react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"

export type PromptDialogMode = "add" | "replace"

export function PromptConfirmDialog({
  mode,
  open,
  onOpenChange,
  initialText = "",
  submitting,
  errorMessage,
  onSubmit,
}: {
  mode: PromptDialogMode
  open: boolean
  onOpenChange: (open: boolean) => void
  // Replace mode pre-fills the current prompt's text (editable); add mode is empty.
  initialText?: string
  submitting: boolean
  errorMessage?: string
  onSubmit: (text: string) => void
}) {
  const [text, setText] = useState(initialText)

  // Reset the field to the mode's starting text each time the dialog opens, so a
  // reopened replace dialog shows the current prompt, not a stale edit. This is
  // the "adjust state during render on a prop change" pattern, not an effect.
  const [wasOpen, setWasOpen] = useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) setText(initialText)
  }

  const trimmed = text.trim()
  const canSubmit = trimmed !== "" && !submitting

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {mode === "add" ? "Add prompt" : "Replace prompt"}
          </DialogTitle>
          {mode === "replace" && (
            <DialogDescription>
              history for the old prompt stays viewable; the new prompt starts a
              fresh trend
            </DialogDescription>
          )}
        </DialogHeader>

        <label className="flex flex-col gap-1.5 text-sm font-medium">
          Prompt
          <Input
            value={text}
            autoFocus
            placeholder="e.g. best clinic near me"
            onChange={(e) => setText(e.currentTarget.value)}
            aria-invalid={trimmed === ""}
            onKeyDown={(e) => {
              if (e.key === "Enter" && canSubmit) onSubmit(trimmed)
            }}
          />
        </label>

        {errorMessage && (
          <p className="text-sm text-destructive" role="alert">
            {errorMessage}
          </p>
        )}

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={submitting}
          >
            Cancel
          </Button>
          <Button disabled={!canSubmit} onClick={() => onSubmit(trimmed)}>
            {mode === "add" ? "Add prompt" : "Replace prompt"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
