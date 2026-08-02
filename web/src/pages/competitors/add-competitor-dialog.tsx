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
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"

export interface AddCompetitorInput {
  name: string
  aliases: string[]
  website: string
}

export function AddCompetitorDialog({
  open,
  onOpenChange,
  submitting,
  errorMessage: addErrorMessage,
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
    onSubmit({
      name: trimmedName,
      aliases: aliases
        .split(",")
        .map((alias) => alias.trim())
        .filter(Boolean),
      website: website.trim(),
    })
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

        <FieldGroup className="gap-4">
          <Field>
            <FieldLabel htmlFor="competitor-name">Name</FieldLabel>
            <Input
              id="competitor-name"
              value={name}
              autoFocus
              required
              placeholder="e.g. Rival Clinic"
              onChange={(event) => setName(event.currentTarget.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") submit()
              }}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="competitor-aliases">Aliases</FieldLabel>
            <Input
              id="competitor-aliases"
              value={aliases}
              placeholder="Rival Health, Rival Medical"
              onChange={(event) => setAliases(event.currentTarget.value)}
            />
            <FieldDescription>Optional, separated by commas.</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="competitor-website">Website</FieldLabel>
            <Input
              id="competitor-website"
              type="url"
              value={website}
              placeholder="https://example.com"
              onChange={(event) => setWebsite(event.currentTarget.value)}
            />
            <FieldDescription>Optional.</FieldDescription>
          </Field>
        </FieldGroup>

        {addErrorMessage && <FieldError>{addErrorMessage}</FieldError>}

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
