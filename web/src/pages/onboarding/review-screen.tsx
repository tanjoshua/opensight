// Review & apply screen (ONB-5, design 03 "Review and apply"): every proposed
// value is editable; nothing is committed until the user applies, and the client
// sends the final edited payload verbatim (the server does not merge). Reused for
// manual setup (failed generation) by seeding an empty payload.
import { useState, type ReactNode } from "react"
import {
  ArrowRight,
  Plus,
  RefreshCw,
  Trash2,
  TriangleAlert,
} from "lucide-react"

import { ApiError } from "@/api/client"
import {
  PROMPT_KINDS,
  type ProposalPayload,
  type ProposedPractitioner,
  type ProposedPrompt,
  useApplyProposal,
} from "@/api/onboarding"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

export function ReviewScreen({
  businessId,
  payload,
  promptLimit,
  onRegenerate,
  regenerating,
  canRegenerate,
  onApplied,
}: {
  businessId: string
  payload: ProposalPayload
  promptLimit: number
  onRegenerate: () => void
  regenerating: boolean
  canRegenerate: boolean
  onApplied: () => void
}) {
  const [draft, setDraft] = useState<ProposalPayload>(payload)
  const apply = useApplyProposal(businessId)

  const profile = draft.profile
  const setProfile = (patch: Partial<ProposalPayload["profile"]>) =>
    setDraft((d) => ({ ...d, profile: { ...d.profile, ...patch } }))

  const validationError = validateFinalPayload(draft, promptLimit)
  const canApply = validationError === undefined

  const applyError =
    apply.error instanceof ApiError
      ? apply.error.status === 404
        ? "Applying isn't wired up yet (arrives in the next step). Your edits are ready to submit."
        : apply.error.message
      : apply.isError
        ? "Could not apply. Try again."
        : undefined

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="font-heading text-xl font-semibold">
            Review your setup
          </h1>
          <p className="text-sm text-muted-foreground">
            Edit anything below. Nothing is monitored until you apply — these
            become your confirmed values.
          </p>
        </div>
        {canRegenerate && (
          <Button
            type="button"
            variant="outline"
            className="ms-auto"
            onClick={onRegenerate}
            disabled={regenerating}
          >
            <RefreshCw data-icon="inline-start" />
            {regenerating ? "Regenerating" : "Regenerate"}
          </Button>
        )}
      </div>

      {draft.low_confidence && (
        <Alert variant="destructive">
          <TriangleAlert />
          <AlertTitle>Review these values carefully</AlertTitle>
          <AlertDescription>
            We had limited information about this business, so the proposal may
            be incomplete or inaccurate. Check every field before applying.
          </AlertDescription>
        </Alert>
      )}

      <Section title="Business profile">
        <Field label="Name">
          <Input
            value={profile.name}
            onChange={(e) => setProfile({ name: e.currentTarget.value })}
            aria-invalid={profile.name.trim() === ""}
          />
        </Field>
        <Field label="Category">
          <Input
            value={profile.category}
            placeholder="e.g. orthopaedic clinic"
            onChange={(e) => setProfile({ category: e.currentTarget.value })}
          />
        </Field>
        <Field
          label="Aliases"
          hint="Other trading names this business is known by — not people's names."
        >
          <StringList
            values={profile.aliases}
            placeholder="Alias"
            addLabel="Add alias"
            onChange={(aliases) => setProfile({ aliases })}
          />
        </Field>
      </Section>

      <Section title="Location">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Address">
            <Input
              value={profile.location.address}
              onChange={(e) =>
                setProfile({
                  location: {
                    ...profile.location,
                    address: e.currentTarget.value,
                  },
                })
              }
            />
          </Field>
          <Field label="Area">
            <Input
              value={profile.location.area}
              placeholder="e.g. Novena"
              onChange={(e) =>
                setProfile({
                  location: {
                    ...profile.location,
                    area: e.currentTarget.value,
                  },
                })
              }
            />
          </Field>
          <Field label="City">
            <Input
              value={profile.location.city}
              onChange={(e) =>
                setProfile({
                  location: {
                    ...profile.location,
                    city: e.currentTarget.value,
                  },
                })
              }
            />
          </Field>
          <Field label="Country" hint="ISO code, e.g. SG.">
            <Input
              value={profile.location.country}
              onChange={(e) =>
                setProfile({
                  location: {
                    ...profile.location,
                    country: e.currentTarget.value,
                  },
                })
              }
            />
          </Field>
        </div>
      </Section>

      <Section
        title="Practitioners"
        action={
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() =>
              setDraft((d) => ({
                ...d,
                profile: {
                  ...d.profile,
                  practitioners: [
                    ...d.profile.practitioners,
                    { name: "", role: "" },
                  ],
                },
              }))
            }
          >
            <Plus data-icon="inline-start" />
            Add practitioner
          </Button>
        }
      >
        {profile.practitioners.length === 0 ? (
          <EmptyRow>No practitioners yet.</EmptyRow>
        ) : (
          <div className="flex flex-col gap-2">
            {profile.practitioners.map((p, i) => (
              <PractitionerRow
                key={i}
                value={p}
                onChange={(next) =>
                  setProfile({
                    practitioners: replaceAt(profile.practitioners, i, next),
                  })
                }
                onRemove={() =>
                  setProfile({
                    practitioners: removeAt(profile.practitioners, i),
                  })
                }
              />
            ))}
          </div>
        )}
      </Section>

      <Section title="Services">
        <StringList
          values={profile.services}
          placeholder="Service"
          addLabel="Add service"
          onChange={(services) => setProfile({ services })}
        />
      </Section>

      <Section
        title="Prompts"
        subtitle={`${draft.prompts.length} monitoring ${
          draft.prompts.length === 1 ? "prompt" : "prompts"
        } — how patients search, without naming your business.`}
        action={
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() =>
              setDraft((d) => ({
                ...d,
                prompts: [...d.prompts, { text: "", kind: PROMPT_KINDS[0] }],
              }))
            }
          >
            <Plus data-icon="inline-start" />
            Add prompt
          </Button>
        }
      >
        {draft.prompts.length === 0 ? (
          <EmptyRow>
            No prompts yet. Add the questions patients would ask.
          </EmptyRow>
        ) : (
          <div className="flex flex-col gap-2">
            {draft.prompts.map((p, i) => (
              <PromptRow
                key={i}
                index={i}
                value={p}
                onChange={(next) =>
                  setDraft((d) => ({
                    ...d,
                    prompts: replaceAt(d.prompts, i, next),
                  }))
                }
                onRemove={() =>
                  setDraft((d) => ({ ...d, prompts: removeAt(d.prompts, i) }))
                }
              />
            ))}
          </div>
        )}
      </Section>

      <div className="sticky bottom-0 -mx-6 flex flex-col gap-2 border-t bg-background/95 px-6 py-4 backdrop-blur">
        {applyError && (
          <p className="text-sm text-destructive" role="alert">
            {applyError}
          </p>
        )}
        <div className="flex flex-wrap items-center gap-3">
          <div className="text-sm text-muted-foreground">
            {validationError ??
              "Applying confirms these values and starts monitoring."}
          </div>
          <Button
            type="button"
            className="ms-auto"
            disabled={!canApply || apply.isPending}
            onClick={() => apply.mutate(draft, { onSuccess: onApplied })}
          >
            {apply.isPending ? "Applying" : "Apply and start monitoring"}
            <ArrowRight data-icon="inline-end" />
          </Button>
        </div>
      </div>
    </div>
  )
}

function validateFinalPayload(
  payload: ProposalPayload,
  promptLimit: number
): string | undefined {
  const profile = payload.profile
  if (profile.name.trim() === "") return "Add a business name to continue."
  if (profile.category.trim() === "")
    return "Add a business category to continue."
  if (!/^[A-Za-z]{2}$/.test(profile.location.country.trim()))
    return "Add a two-letter country code to continue."
  if (profile.aliases.some((alias) => alias.trim() === ""))
    return "Complete or remove every alias to continue."
  if (profile.services.some((service) => service.trim() === ""))
    return "Complete or remove every service to continue."
  if (
    profile.practitioners.some(
      (practitioner) => practitioner.name.trim() === ""
    )
  )
    return "Add a name for every practitioner, or remove the row."
  if (payload.prompts.length !== promptLimit)
    return `Add exactly ${promptLimit} prompts to continue.`
  if (payload.prompts.some((prompt) => prompt.text.trim() === ""))
    return "Complete or remove every prompt to continue."
  if (
    payload.prompts.some(
      (prompt) =>
        !PROMPT_KINDS.includes(prompt.kind as (typeof PROMPT_KINDS)[number])
    )
  )
    return "Choose a valid kind for every prompt to continue."
  return undefined
}

function Section({
  title,
  subtitle,
  action,
  children,
}: {
  title: string
  subtitle?: string
  action?: ReactNode
  children: ReactNode
}) {
  return (
    <section className="flex flex-col gap-4 rounded-2xl border p-5">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex flex-col gap-0.5">
          <h2 className="font-heading text-base font-medium">{title}</h2>
          {subtitle && (
            <p className="text-sm text-muted-foreground">{subtitle}</p>
          )}
        </div>
        {action && <div className="ms-auto">{action}</div>}
      </div>
      {children}
    </section>
  )
}

function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: ReactNode
}) {
  return (
    <label className="flex flex-col gap-1.5 text-sm font-medium">
      {label}
      {children}
      {hint && (
        <span className="text-xs font-normal text-muted-foreground">
          {hint}
        </span>
      )}
    </label>
  )
}

function EmptyRow({ children }: { children: ReactNode }) {
  return (
    <p className="rounded-xl border border-dashed px-3 py-4 text-center text-sm text-muted-foreground">
      {children}
    </p>
  )
}

// StringList edits an array of plain strings (aliases, services). An empty row
// is appended on "Add"; a row cleared to empty and removed drops out.
function StringList({
  values,
  placeholder,
  addLabel,
  onChange,
}: {
  values: string[]
  placeholder: string
  addLabel: string
  onChange: (values: string[]) => void
}) {
  return (
    <div className="flex flex-col gap-2">
      {values.map((value, i) => (
        <div key={i} className="flex items-center gap-2">
          <Input
            value={value}
            placeholder={placeholder}
            onChange={(e) =>
              onChange(replaceAt(values, i, e.currentTarget.value))
            }
          />
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={`Remove ${placeholder.toLowerCase()}`}
            onClick={() => onChange(removeAt(values, i))}
          >
            <Trash2 />
          </Button>
        </div>
      ))}
      <div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onChange([...values, ""])}
        >
          <Plus data-icon="inline-start" />
          {addLabel}
        </Button>
      </div>
    </div>
  )
}

function PractitionerRow({
  value,
  onChange,
  onRemove,
}: {
  value: ProposedPractitioner
  onChange: (value: ProposedPractitioner) => void
  onRemove: () => void
}) {
  return (
    <div className="flex flex-wrap items-center gap-2 sm:flex-nowrap">
      <Input
        className="sm:flex-[2]"
        value={value.name}
        placeholder="Name"
        onChange={(e) => onChange({ ...value, name: e.currentTarget.value })}
      />
      <Input
        className="sm:flex-[1]"
        value={value.role}
        placeholder="Role"
        onChange={(e) => onChange({ ...value, role: e.currentTarget.value })}
      />
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        aria-label="Remove practitioner"
        onClick={onRemove}
      >
        <Trash2 />
      </Button>
    </div>
  )
}

function PromptRow({
  index,
  value,
  onChange,
  onRemove,
}: {
  index: number
  value: ProposedPrompt
  onChange: (value: ProposedPrompt) => void
  onRemove: () => void
}) {
  return (
    <div className="flex flex-wrap items-start gap-2 sm:flex-nowrap">
      <Badge variant="outline" className="mt-1.5 shrink-0">
        {index + 1}
      </Badge>
      <Input
        className="flex-[3]"
        value={value.text}
        placeholder="e.g. best orthopaedic clinic in Singapore"
        onChange={(e) => onChange({ ...value, text: e.currentTarget.value })}
      />
      <KindSelect
        value={value.kind}
        onChange={(kind) => onChange({ ...value, kind })}
      />
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        aria-label="Remove prompt"
        onClick={onRemove}
      >
        <Trash2 />
      </Button>
    </div>
  )
}

function KindSelect({
  value,
  onChange,
}: {
  value: string
  onChange: (value: string) => void
}) {
  // A generated kind outside the closed set still needs to render; show it as-is.
  const items = PROMPT_KINDS.map((k) => ({ label: k, value: k }))
  return (
    <Select
      items={items}
      value={value}
      onValueChange={(v) => onChange(String(v))}
    >
      <SelectTrigger size="sm" className="shrink-0" aria-label="Prompt kind">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          {items.map((item) => (
            <SelectItem key={item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  )
}

function replaceAt<T>(arr: T[], index: number, value: T): T[] {
  const next = arr.slice()
  next[index] = value
  return next
}

function removeAt<T>(arr: T[], index: number): T[] {
  return arr.filter((_, i) => i !== index)
}
