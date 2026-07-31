import {
  createConnectQueryKey,
  skipToken,
  useMutation,
  useQuery,
} from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { useState, type FormEvent } from "react"
import { Link, Navigate } from "react-router"

import { errorMessage } from "@/api/errors"
import {
  useAllCompetitors,
  useCurrentBusiness,
  useInvalidateCompetitorViews,
  usePlan,
} from "@/api/hooks"
import { getMe } from "@/gen/opensight/v1/auth-AuthService_connectquery"
import { BusinessStatus } from "@/gen/opensight/v1/common_pb"
import type { BusinessProfile } from "@/gen/opensight/v1/business_pb"
import {
  getBusiness,
  updateBusiness,
} from "@/gen/opensight/v1/business-BusinessService_connectquery"
import type { Competitor } from "@/gen/opensight/v1/competitor_pb"
import { updateCompetitorAliases } from "@/gen/opensight/v1/competitor-CompetitorService_connectquery"
import { Button } from "@/components/ui/button"
import { PageHeader } from "@/components/page-header"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"

export function SetupPage() {
  const current = useCurrentBusiness()
  const summary = current.business
  const business = useQuery(
    getBusiness,
    summary === undefined ? skipToken : { businessId: summary.id }
  )
  const competitors = useAllCompetitors(summary?.id)

  if (current.isLoading || business.isLoading || competitors.isLoading) {
    return <SetupSkeleton />
  }
  if (
    current.isError ||
    business.isError ||
    competitors.isError ||
    !current.isReady
  ) {
    return (
      <p role="alert">Setup could not be loaded. Try reloading the page.</p>
    )
  }
  if (!summary) {
    return <Navigate to="/onboarding" replace />
  }
  if (
    summary.status === BusinessStatus.DRAFT ||
    business.data?.business?.status === BusinessStatus.DRAFT
  ) {
    return <Navigate to="/onboarding" replace />
  }
  if (!business.data?.business || !competitors.data) return <SetupSkeleton />

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Settings"
        description="Manage the confirmed values used by future monitoring runs."
      />
      <ProfileEditor
        key={business.data.business.id}
        business={business.data.business}
      />
      <Card>
        <CardHeader>
          <CardTitle>Prompts</CardTitle>
          <CardDescription>
            Add or replace the questions measured in future runs.
          </CardDescription>
          <CardAction>
            <Button render={<Link to="/prompts" />}>Manage prompts</Button>
          </CardAction>
        </CardHeader>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Competitor aliases</CardTitle>
          <CardDescription>
            Approved names used for exact matching. Suggested aliases remain in
            Competitors until reviewed.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {competitors.data.competitors.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No competitors to configure yet.
            </p>
          ) : (
            competitors.data.competitors.map((competitor) => (
              <CompetitorAliases key={competitor.id} competitor={competitor} />
            ))
          )}
        </CardContent>
      </Card>
      <PlanCard />
    </div>
  )
}

function ProfileEditor({ business }: { business: BusinessProfile }) {
  const queryClient = useQueryClient()
  const mutation = useMutation(updateBusiness, {
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: getBusiness,
          input: { businessId: business.id },
          cardinality: "finite",
        }),
      })
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: getMe,
          cardinality: "finite",
        }),
      })
    },
  })
  const [form, setForm] = useState(() => profileForm(business))
  const [touched, setTouched] = useState<Set<ProfilePatchField>>(
    () => new Set()
  )
  const [saved, setSaved] = useState(false)
  const error = validateProfileForm(form)
  const serverError = mutation.isError
    ? errorMessage(mutation.error, "Profile could not be saved. Try again.")
    : undefined
  const patch = profilePatch(form, business, touched)
  const dirty = Object.keys(patch).length > 0
  const markTouched = (field: ProfilePatchField) =>
    setTouched((current) => new Set(current).add(field))

  const set = (field: ProfileScalar, value: string) => {
    setSaved(false)
    markTouched(
      field === "address" ||
        field === "area" ||
        field === "city" ||
        field === "country"
        ? "location"
        : field
    )
    setForm((current) => ({ ...current, [field]: value }))
  }
  const setList = (field: "aliases" | "services", items: ListItem[]) => {
    setSaved(false)
    markTouched(field)
    setForm((current) => ({ ...current, [field]: items }))
  }
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (error) return
    mutation.mutate(
      {
        businessId: business.id,
        ...patch,
        aliases:
          patch.aliases === undefined ? undefined : { values: patch.aliases },
        services:
          patch.services === undefined ? undefined : { values: patch.services },
      },
      {
        onSuccess: (data) => {
          if (data.business) setForm(profileForm(data.business))
          setTouched(new Set())
          setSaved(true)
        },
      }
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Business profile</CardTitle>
        <CardDescription>
          These confirmed details guide matching and location-aware prompts.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form className="flex flex-col gap-6" onSubmit={submit}>
          <FieldGroup>
            <TextField
              label="Name"
              value={form.name}
              onChange={(v) => set("name", v)}
            />
            <TextField
              label="Website"
              value={form.website}
              onChange={(v) => set("website", v)}
            />
            <TextField
              label="Category"
              value={form.category}
              onChange={(v) => set("category", v)}
            />
            <RepeatableTextFields
              label="Business aliases"
              description="Organization trading names, not people."
              items={form.aliases}
              onChange={(items) => setList("aliases", items)}
            />
            <RepeatableTextFields
              label="Services"
              items={form.services}
              onChange={(items) => setList("services", items)}
            />
            <TextField
              label="Address"
              value={form.address}
              onChange={(v) => set("address", v)}
            />
            <TextField
              label="Area"
              value={form.area}
              onChange={(v) => set("area", v)}
            />
            <TextField
              label="City"
              value={form.city}
              onChange={(v) => set("city", v)}
            />
            <TextField
              label="Country"
              description="Two-letter ISO code, such as SG."
              value={form.country}
              onChange={(v) => set("country", v)}
              invalid={!/^[A-Za-z]{2}$/.test(form.country.trim())}
            />
          </FieldGroup>
          {(error || serverError) && (
            <FieldError>{error ?? serverError}</FieldError>
          )}
          <p
            role="status"
            aria-live="polite"
            className="text-sm text-muted-foreground"
          >
            {mutation.isPending
              ? "Saving profile…"
              : saved
                ? "Profile saved."
                : ""}
          </p>
          <Button
            type="submit"
            disabled={!dirty || Boolean(error) || mutation.isPending}
          >
            Save profile
          </Button>
        </form>
      </CardContent>
    </Card>
  )
}

function RepeatableTextFields({
  label,
  description,
  items,
  onChange,
}: {
  label: string
  description?: string
  items: ListItem[]
  onChange: (items: ListItem[]) => void
}) {
  const prefix = label.toLowerCase().replaceAll(" ", "-")
  return (
    <FieldSet>
      <FieldLegend variant="label">{label}</FieldLegend>
      {description && <FieldDescription>{description}</FieldDescription>}
      <div className="flex flex-col gap-2">
        {items.map((item, index) => {
          const id = `profile-${prefix}-${item.key}`
          return (
            <div key={item.key} className="flex gap-2">
              <Input
                id={id}
                aria-label={`${label} ${index + 1}`}
                value={item.value}
                onChange={(event) =>
                  onChange(
                    items.map((current) =>
                      current.key === item.key
                        ? { ...current, value: event.currentTarget.value }
                        : current
                    )
                  )
                }
              />
              <Button
                type="button"
                variant="outline"
                aria-label={`Remove ${label.toLowerCase()} ${index + 1}`}
                onClick={() =>
                  onChange(items.filter((current) => current.key !== item.key))
                }
              >
                Remove
              </Button>
            </div>
          )
        })}
      </div>
      <Button
        type="button"
        variant="outline"
        className="w-fit"
        onClick={() => onChange([...items, listItem("")])}
      >
        Add {label.toLowerCase().replace(/s$/, "")}
      </Button>
    </FieldSet>
  )
}

function TextField({
  label,
  description,
  value,
  onChange,
  invalid = false,
}: {
  label: string
  description?: string
  value: string
  onChange: (value: string) => void
  invalid?: boolean
}) {
  const id = `profile-${label.toLowerCase().replaceAll(" ", "-")}`
  return (
    <Field data-invalid={invalid || undefined}>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <Input
        id={id}
        value={value}
        aria-invalid={invalid || undefined}
        onChange={(event) => onChange(event.currentTarget.value)}
      />
      {description && <FieldDescription>{description}</FieldDescription>}
    </Field>
  )
}

function CompetitorAliases({ competitor }: { competitor: Competitor }) {
  const invalidateCompetitorViews = useInvalidateCompetitorViews()
  const mutation = useMutation(updateCompetitorAliases, {
    onSuccess: invalidateCompetitorViews,
  })
  const [items, setItems] = useState(() => competitor.aliases.map(listItem))
  const [saved, setSaved] = useState(false)
  const aliases = items.map((item) => item.value)
  const dirty = JSON.stringify(aliases) !== JSON.stringify(competitor.aliases)
  const error = mutation.isError
    ? errorMessage(mutation.error, "Aliases could not be saved.")
    : undefined
  return (
    <FieldSet>
      <FieldLegend variant="label">
        {competitor.name} approved aliases
      </FieldLegend>
      {items.map((item, index) => (
        <div key={item.key} className="flex gap-2">
          <Input
            aria-label={`${competitor.name} approved alias ${index + 1}`}
            value={item.value}
            onChange={(event) => {
              setSaved(false)
              setItems((current) =>
                current.map((value) =>
                  value.key === item.key
                    ? { ...value, value: event.currentTarget.value }
                    : value
                )
              )
            }}
          />
          <Button
            type="button"
            variant="outline"
            aria-label={`Remove approved alias ${index + 1} for ${competitor.name}`}
            onClick={() => {
              setSaved(false)
              setItems((current) =>
                current.filter((value) => value.key !== item.key)
              )
            }}
          >
            Remove
          </Button>
        </div>
      ))}
      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          variant="outline"
          onClick={() => {
            setSaved(false)
            setItems((current) => [...current, listItem("")])
          }}
        >
          Add alias
        </Button>
        <Button
          type="button"
          variant="outline"
          aria-label={`Save approved aliases for ${competitor.name}`}
          disabled={!dirty || mutation.isPending}
          onClick={() =>
            mutation.mutate(
              { competitorId: competitor.id, aliases: { values: aliases } },
              {
                onSuccess: (updated) => {
                  if (updated.competitor) {
                    setItems(updated.competitor.aliases.map(listItem))
                  }
                  setSaved(true)
                },
              }
            )
          }
        >
          Save
        </Button>
      </div>
      {error && <FieldError>{error}</FieldError>}
      <p
        role="status"
        aria-live="polite"
        className="text-sm text-muted-foreground"
      >
        {mutation.isPending ? "Saving aliases…" : saved ? "Aliases saved." : ""}
      </p>
    </FieldSet>
  )
}

// PlanCard reads from useMe() (via usePlan(), BILL-6), not the business:
// plan entitlements are a tenant fact, not a per-business one.
function PlanCard() {
  const { plan } = usePlan()
  if (!plan) return null
  return (
    <Card>
      <CardHeader>
        <CardTitle>Plan</CardTitle>
        <CardDescription>
          Current read-only monitoring entitlements.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3 sm:grid-cols-2">
        <PlanValue label="Plan" value={plan.code} />
        <PlanValue label="Prompt limit" value={String(plan.promptLimit)} />
        <PlanValue label="Run interval" value={plan.runInterval} />
        <PlanValue label="Platforms" value={plan.platforms.join(", ")} />
      </CardContent>
    </Card>
  )
}

function PlanValue({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="font-medium">{value}</p>
    </div>
  )
}

interface ListItem {
  key: string
  value: string
}

// Local patch shape the form builds — profilePatch below turns "aliases"/
// "services" into the StringList wrapper UpdateBusinessRequest expects only
// at the mutate() call site, since proto3 optional presence (omitted =
// unchanged) doesn't map cleanly onto a plain array field.
interface BusinessProfilePatch {
  name?: string
  website?: string
  aliases?: string[]
  category?: string
  services?: string[]
  location?: { address: string; area: string; city: string; country: string }
}

type ProfileForm = ReturnType<typeof profileForm>
type ProfileScalar = Exclude<keyof ProfileForm, "aliases" | "services">
type ProfilePatchField = keyof BusinessProfilePatch

let fieldKey = 0
function nextKey() {
  fieldKey += 1
  return String(fieldKey)
}

function listItem(value: string): ListItem {
  return { key: nextKey(), value }
}

function profileForm(business: BusinessProfile) {
  const location = business.location
  return {
    name: business.name,
    website: business.website ?? "",
    category: business.category ?? "",
    aliases: business.aliases.map(listItem),
    services: business.services.map(listItem),
    address: location?.address ?? "",
    area: location?.area ?? "",
    city: location?.city ?? "",
    country: location?.country ?? "",
  }
}

function profilePatch(
  form: ProfileForm,
  business: BusinessProfile,
  touched: Set<ProfilePatchField>
): BusinessProfilePatch {
  const patch: BusinessProfilePatch = {}
  if (touched.has("name") && form.name !== business.name) patch.name = form.name
  if (touched.has("website") && form.website !== (business.website ?? "")) {
    patch.website = form.website
  }
  if (touched.has("category") && form.category !== (business.category ?? "")) {
    patch.category = form.category
  }

  const aliases = form.aliases.map((item) => item.value)
  if (
    touched.has("aliases") &&
    JSON.stringify(aliases) !== JSON.stringify(business.aliases)
  ) {
    patch.aliases = aliases
  }
  const services = form.services.map((item) => item.value)
  if (
    touched.has("services") &&
    JSON.stringify(services) !== JSON.stringify(business.services)
  ) {
    patch.services = services
  }
  const location = {
    address: form.address,
    area: form.area,
    city: form.city,
    country: form.country,
  }
  if (
    touched.has("location") &&
    JSON.stringify(location) !== JSON.stringify(business.location)
  ) {
    patch.location = location
  }
  return patch
}

function validateProfileForm(form: ProfileForm) {
  if (form.name.trim() === "") return "Name is required."
  if (form.category.trim() === "") return "Category is required."
  if (!/^[A-Za-z]{2}$/.test(form.country.trim())) {
    return "Country must be a two-letter ISO code."
  }
  if (form.aliases.some((item) => item.value.trim() === "")) {
    return "Aliases cannot be blank."
  }
  if (form.services.some((item) => item.value.trim() === "")) {
    return "Services cannot be blank."
  }
}

function SetupSkeleton() {
  return (
    <div className="flex flex-col gap-6">
      <Skeleton className="h-12 w-64" />
      <Skeleton className="h-96 w-full" />
      <Skeleton className="h-40 w-full" />
    </div>
  )
}
