import { type FormEvent, useState } from "react"
import { Globe2, Pencil } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

export function ResearchWebsite({
  website,
  onChange,
  pending,
  confirmBeforeChange = false,
}: {
  website: string
  onChange: (website: string) => void
  pending: boolean
  confirmBeforeChange?: boolean
}) {
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState(website)
  const changed = value.trim() !== website

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!changed) return
    if (
      confirmBeforeChange &&
      !window.confirm(
        "Changing the website will regenerate your setup and replace edits on this screen. Continue?"
      )
    ) {
      return
    }
    onChange(value.trim())
    setEditing(false)
  }

  if (editing) {
    return (
      <form
        className="flex flex-col gap-3 rounded-2xl border bg-muted/30 p-4"
        onSubmit={submit}
      >
        <label className="flex flex-col gap-1.5 text-sm font-medium">
          Website (optional)
          <Input
            type="url"
            value={value}
            placeholder="https://example.com"
            onChange={(event) => setValue(event.currentTarget.value)}
            disabled={pending}
            autoFocus
          />
        </label>
        <p className="text-xs text-muted-foreground">
          We’ll restart the research so the generated setup uses this website.
        </p>
        <div className="flex flex-wrap gap-2">
          <Button type="submit" size="sm" disabled={pending || !changed}>
            {pending ? "Restarting" : "Save and research again"}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            onClick={() => {
              setValue(website)
              setEditing(false)
            }}
            disabled={pending}
          >
            Cancel
          </Button>
        </div>
      </form>
    )
  }

  return (
    <div className="flex flex-wrap items-center gap-3 rounded-2xl border bg-muted/30 p-4 text-sm">
      <Globe2 className="size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1">
        <span className="text-muted-foreground">Research website</span>
        <p className="truncate font-medium">
          {website || "No website provided"}
        </p>
      </div>
      <Button
        type="button"
        size="sm"
        variant="outline"
        onClick={() => {
          setValue(website)
          setEditing(true)
        }}
        disabled={pending}
      >
        <Pencil data-icon="inline-start" />
        {website ? "Change" : "Add website"}
      </Button>
    </div>
  )
}
