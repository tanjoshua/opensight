// Review & apply screen (ONB-5, design 03 "Review and apply"): every proposed
// value is editable; nothing is committed until the user applies, and the client
// sends the final edited payload verbatim (the server does not merge). Reused for
// manual setup (failed generation) by seeding an empty payload.
import { create } from "@bufbuild/protobuf"
import { useMutation } from "@connectrpc/connect-query"
import { useState, type ReactNode } from "react"
import {
  ArrowLeft,
  ArrowRight,
  Check,
  ExternalLink,
  Plus,
  RefreshCw,
  Trash2,
  TriangleAlert,
} from "lucide-react"

import { errorMessage } from "@/api/errors"
import { cn } from "@/lib/utils"
import {
  ProposedProfileSchema,
  ProposedPromptSchema,
  type ProposalPayload,
  type ProposedProfile,
  type ProposedPrompt,
} from "@/gen/opensight/v1/business_pb"
import { applyProposal } from "@/gen/opensight/v1/business-BusinessService_connectquery"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

const EMPTY_LOCATION = { address: "", area: "", city: "", country: "" }

const EMPTY_PROFILE: ProposedProfile = create(ProposedProfileSchema, {
  name: "",
  aliases: [],
  category: "",
  services: [],
  location: EMPTY_LOCATION,
})

export function ReviewScreen({
  businessId,
  payload,
  promptLimit,
  onRegenerate,
  regenerating,
  regenError,
  canRegenerate,
  onApplied,
}: {
  businessId: string
  payload: ProposalPayload
  promptLimit: number
  onRegenerate: () => void
  regenerating: boolean
  regenError?: string
  canRegenerate: boolean
  onApplied: () => void
}) {
  const [draft, setDraft] = useState<ProposalPayload>(payload)
  const [step, setStep] = useState(0)
  const [hasLocalEdits, setHasLocalEdits] = useState(false)
  const apply = useMutation(applyProposal, {
    onSuccess: onApplied,
  })

  // profile is always populated in practice (EMPTY_PAYLOAD sets it, and a
  // ready/failed proposal payload always carries one); the empty fallback
  // keeps the form rendering rather than special-casing an impossible state.
  const profile = draft.profile ?? EMPTY_PROFILE
  const location = profile.location ?? EMPTY_LOCATION
  const updateDraft = (
    update: (current: ProposalPayload) => ProposalPayload
  ) => {
    setHasLocalEdits(true)
    setDraft(update)
  }
  const setProfile = (patch: {
    name?: string
    category?: string
    aliases?: string[]
    services?: string[]
    location?: { address: string; area: string; city: string; country: string }
  }) =>
    updateDraft((d) => {
      const current = d.profile ?? EMPTY_PROFILE
      return {
        ...d,
        profile: create(ProposedProfileSchema, {
          name: patch.name ?? current.name,
          category: patch.category ?? current.category,
          aliases: patch.aliases ?? current.aliases,
          services: patch.services ?? current.services,
          location: patch.location ?? current.location,
        }),
      }
    })

  const validationError = validateFinalPayload(draft, promptLimit)
  const canApply = validationError === undefined
  const stepError = validateStep(step, draft, promptLimit)

  const applyError = apply.isError
    ? errorMessage(apply.error, "Could not apply. Try again.")
    : undefined

  const regenerate = () => {
    if (
      hasLocalEdits &&
      !window.confirm(
        "Regenerating will replace the edits you made on this screen. Continue?"
      )
    ) {
      return
    }
    onRegenerate()
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="font-heading text-xl font-semibold">
            Review your setup
          </h1>
          <p className="text-sm text-muted-foreground">
            Review each step before monitoring starts. Your edits stay on this
            screen until you approve the setup.
          </p>
        </div>
        {canRegenerate && (
          <Button
            type="button"
            variant="outline"
            className="ms-auto"
            onClick={regenerate}
            disabled={regenerating}
          >
            <RefreshCw data-icon="inline-start" />
            {regenerating ? "Regenerating" : "Regenerate"}
          </Button>
        )}
        {regenError && (
          <p className="w-full text-sm text-destructive" role="alert">
            {regenError}
          </p>
        )}
      </div>

      {draft.lowConfidence && (
        <Alert variant="destructive">
          <TriangleAlert />
          <AlertTitle>Review these values carefully</AlertTitle>
          <AlertDescription>
            We had limited information about this business, so the proposal may
            be incomplete or inaccurate. Check every field before approving.
          </AlertDescription>
        </Alert>
      )}

      <StepIndicator current={step} />

      {step === 0 && (
        <Section
          title="Business"
          subtitle="Confirm the identity and location we will monitor."
        >
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
              aria-invalid={profile.category.trim() === ""}
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
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Address">
              <Input
                value={location.address}
                onChange={(e) =>
                  setProfile({
                    location: {
                      ...location,
                      address: e.currentTarget.value,
                    },
                  })
                }
              />
            </Field>
            <Field label="Area">
              <Input
                value={location.area}
                placeholder="e.g. Novena"
                onChange={(e) =>
                  setProfile({
                    location: {
                      ...location,
                      area: e.currentTarget.value,
                    },
                  })
                }
              />
            </Field>
            <Field label="City">
              <Input
                value={location.city}
                onChange={(e) =>
                  setProfile({
                    location: {
                      ...location,
                      city: e.currentTarget.value,
                    },
                  })
                }
              />
            </Field>
            <Field label="Country" hint="Two-letter code, e.g. SG.">
              <Input
                value={location.country}
                aria-invalid={!/^[A-Za-z]{2}$/.test(location.country.trim())}
                onChange={(e) =>
                  setProfile({
                    location: {
                      ...location,
                      country: e.currentTarget.value,
                    },
                  })
                }
              />
            </Field>
          </div>
          {payload.sources.length > 0 && (
            <details className="rounded-lg border px-4 py-3">
              <summary className="cursor-pointer text-sm font-medium">
                Research sources ({payload.sources.length})
              </summary>
              <ul className="mt-3 flex flex-col gap-2">
                {payload.sources.map((source, i) => (
                  <li key={i}>
                    <a
                      href={source.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex min-h-11 items-center gap-1.5 text-sm text-primary underline-offset-4 hover:underline"
                    >
                      {source.domain}
                      <ExternalLink className="size-3.5 shrink-0" />
                    </a>
                  </li>
                ))}
              </ul>
            </details>
          )}
        </Section>
      )}

      {step === 1 && (
        <Section
          title="Services"
          subtitle="Add the services customers might search for."
        >
          <StringList
            values={profile.services}
            placeholder="Service"
            addLabel="Add service"
            onChange={(services) => setProfile({ services })}
          />
        </Section>
      )}

      {step === 2 && (
        <Section
          title="Customer questions"
          subtitle={`${draft.prompts.length} of ${promptLimit} questions. Add exactly ${promptLimit} to start monitoring.`}
          action={
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() =>
                updateDraft((d) => ({
                  ...d,
                  prompts: [
                    ...d.prompts,
                    create(ProposedPromptSchema, { text: "" }),
                  ],
                }))
              }
            >
              <Plus data-icon="inline-start" />
              Add question
            </Button>
          }
        >
          {draft.prompts.length === 0 ? (
            <EmptyRow>
              No questions yet. Add the questions customers would ask.
            </EmptyRow>
          ) : (
            <div className="flex flex-col gap-2">
              {draft.prompts.map((p, i) => (
                <PromptRow
                  key={i}
                  index={i}
                  value={p}
                  onChange={(next) =>
                    updateDraft((d) => ({
                      ...d,
                      prompts: replaceAt(d.prompts, i, next),
                    }))
                  }
                  onRemove={() =>
                    updateDraft((d) => ({
                      ...d,
                      prompts: removeAt(d.prompts, i),
                    }))
                  }
                />
              ))}
            </div>
          )}
        </Section>
      )}

      <div className="sticky bottom-0 -mx-6 flex flex-col gap-2 border-t bg-background/95 px-6 py-4 backdrop-blur">
        {applyError && (
          <p className="text-sm text-destructive" role="alert">
            {applyError}
          </p>
        )}
        <div className="flex flex-wrap items-center gap-3">
          <div className="text-sm text-muted-foreground">
            {stepError ??
              (step === 2
                ? "Your first check starts after approval; monitoring then runs weekly."
                : "Your edits are retained when you move between steps.")}
          </div>
          <div className="ms-auto flex items-center gap-2">
            {step > 0 && (
              <Button
                type="button"
                variant="outline"
                onClick={() => setStep((current) => current - 1)}
              >
                <ArrowLeft data-icon="inline-start" />
                Back
              </Button>
            )}
            {step < 2 ? (
              <Button
                type="button"
                disabled={stepError !== undefined}
                onClick={() => setStep((current) => current + 1)}
              >
                Next
                <ArrowRight data-icon="inline-end" />
              </Button>
            ) : (
              <Button
                type="button"
                disabled={!canApply || apply.isPending}
                onClick={() => apply.mutate({ businessId, payload: draft })}
              >
                {apply.isPending
                  ? "Approving"
                  : "Approve setup and start monitoring"}
                <ArrowRight data-icon="inline-end" />
              </Button>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

const STEPS = ["Business", "Services", "Customer questions"] as const

function StepIndicator({ current }: { current: number }) {
  return (
    <nav aria-label="Setup progress">
      <ol className="grid grid-cols-3 gap-2">
        {STEPS.map((label, index) => (
          <li
            key={label}
            aria-current={index === current ? "step" : undefined}
            className="flex min-w-0 items-center gap-2 text-xs sm:text-sm"
          >
            <span
              className={cn(
                "flex size-7 shrink-0 items-center justify-center rounded-full border",
                index <= current
                  ? "border-primary bg-primary text-primary-foreground"
                  : "text-muted-foreground"
              )}
            >
              {index < current ? <Check /> : index + 1}
            </span>
            <span
              className={
                index === current
                  ? "hidden font-medium sm:block"
                  : "hidden text-muted-foreground sm:block"
              }
            >
              {label}
            </span>
          </li>
        ))}
      </ol>
    </nav>
  )
}

function validateStep(
  step: number,
  payload: ProposalPayload,
  promptLimit: number
): string | undefined {
  const profile = payload.profile ?? EMPTY_PROFILE
  const location = profile.location ?? EMPTY_LOCATION
  if (step === 0) {
    if (profile.name.trim() === "") return "Add a business name to continue."
    if (profile.category.trim() === "")
      return "Add a business category to continue."
    if (!/^[A-Za-z]{2}$/.test(location.country.trim()))
      return "Add a two-letter country code to continue."
    if (profile.aliases.some((alias) => alias.trim() === ""))
      return "Complete or remove every alias to continue."
  }
  if (step === 1 && profile.services.some((service) => service.trim() === ""))
    return "Complete or remove every service to continue."
  if (step === 2) {
    if (payload.prompts.length !== promptLimit)
      return `Add exactly ${promptLimit} questions to continue.`
    if (payload.prompts.some((prompt) => prompt.text.trim() === ""))
      return "Complete or remove every question to continue."
  }
  return undefined
}

function validateFinalPayload(
  payload: ProposalPayload,
  promptLimit: number
): string | undefined {
  const profile = payload.profile ?? EMPTY_PROFILE
  const location = profile.location ?? EMPTY_LOCATION
  if (profile.name.trim() === "") return "Add a business name to continue."
  if (profile.category.trim() === "")
    return "Add a business category to continue."
  if (!/^[A-Za-z]{2}$/.test(location.country.trim()))
    return "Add a two-letter country code to continue."
  if (profile.aliases.some((alias) => alias.trim() === ""))
    return "Complete or remove every alias to continue."
  if (profile.services.some((service) => service.trim() === ""))
    return "Complete or remove every service to continue."
  if (payload.prompts.length !== promptLimit)
    return `Add exactly ${promptLimit} prompts to continue.`
  if (payload.prompts.some((prompt) => prompt.text.trim() === ""))
    return "Complete or remove every prompt to continue."
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
        className="flex-1"
        value={value.text}
        placeholder="e.g. best orthopaedic clinic in Singapore"
        onChange={(e) => onChange({ ...value, text: e.currentTarget.value })}
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

function replaceAt<T>(arr: T[], index: number, value: T): T[] {
  const next = arr.slice()
  next[index] = value
  return next
}

function removeAt<T>(arr: T[], index: number): T[] {
  return arr.filter((_, i) => i !== index)
}
