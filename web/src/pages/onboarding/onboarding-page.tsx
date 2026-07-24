// Onboarding flow (ONB-5, design 03): create form → progress state polling the
// generation proposal → review screen with every value editable. A failed
// generation drops into the same review screen, empty, for manual setup. The
// draft is recoverable via /me, so a reload mid-flow resumes rather than
// restarting.
import { type FormEvent, type ReactNode, useState } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import {
  Building2,
  Check,
  LoaderCircle,
  Sparkles,
  TriangleAlert,
} from "lucide-react"
import { Navigate, useNavigate } from "react-router"

import { useMe } from "@/api/auth"
import { ApiError } from "@/api/client"
import {
  createBusiness,
  useProposal,
  useRegenProposal,
  type GenerationStage,
  type ProposalPayload,
} from "@/api/onboarding"
import { ReviewScreen } from "@/pages/onboarding/review-screen"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"

const EMPTY_PAYLOAD: ProposalPayload = {
  low_confidence: false,
  profile: {
    name: "",
    aliases: [],
    category: "",
    services: [],
    location: { address: "", area: "", city: "", country: "" },
  },
  prompts: [],
}

export function OnboardingPage() {
  const me = useMe()
  const [createdId, setCreatedId] = useState<string>()

  if (me.isLoading) {
    return (
      <OnboardingShell>{<Skeleton className="h-64 w-full" />}</OnboardingShell>
    )
  }
  if (me.error instanceof ApiError && me.error.status === 401) {
    return <Navigate to="/login" replace />
  }
  if (me.isError || !me.data) {
    return (
      <OnboardingShell>
        <ProgressState
          icon={<TriangleAlert className="text-destructive" />}
          title="Couldn't load your setup"
          description="We couldn't check your existing setup. Try again before creating a business."
          action={
            <Button type="button" onClick={() => void me.refetch()}>
              Try again
            </Button>
          }
        />
      </OnboardingShell>
    )
  }

  const businesses = me.data.businesses
  const draft = businesses.find((b) => b.status === "draft")
  // MVP is one business per tenant: an already-active business means onboarding
  // is done, so send the user into the app rather than letting them start over.
  const active = businesses.find((b) => b.status !== "draft")
  const businessId = createdId ?? draft?.id

  if (!businessId) {
    if (active) return <Navigate to="/overview" replace />
    return (
      <OnboardingShell>
        <CreateForm onCreated={setCreatedId} />
      </OnboardingShell>
    )
  }

  return (
    <OnboardingShell wide>
      <ProposalFlow
        businessId={businessId}
        promptLimit={me.data.prompt_limit}
      />
    </OnboardingShell>
  )
}

function CreateForm({ onCreated }: { onCreated: (id: string) => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState("")
  const [website, setWebsite] = useState("")

  const create = useMutation({
    mutationFn: createBusiness,
    onSuccess: async (business) => {
      // Refresh /me so the draft is resumable on reload, then enter the flow.
      await queryClient.invalidateQueries({ queryKey: ["me"] })
      onCreated(business.id)
    },
  })

  const error =
    create.error instanceof ApiError
      ? create.error.message
      : create.isError
        ? "Could not start setup. Try again."
        : undefined

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    create.mutate({ name: name.trim(), website: website.trim() })
  }

  return (
    <form className="flex flex-col gap-6" onSubmit={onSubmit}>
      <div className="flex flex-col gap-1">
        <h1 className="font-heading text-xl font-semibold">
          Set up your business
        </h1>
        <p className="text-sm text-muted-foreground">
          Enter your name and website. We'll research it and propose a profile
          and prompts for you to review — this usually takes a few minutes.
        </p>
      </div>

      <label className="flex flex-col gap-1.5 text-sm font-medium">
        Business name
        <Input
          value={name}
          placeholder="e.g. Novena Orthopaedic Clinic"
          onChange={(e) => setName(e.currentTarget.value)}
          disabled={create.isPending}
          required
        />
      </label>
      <label className="flex flex-col gap-1.5 text-sm font-medium">
        Website
        <Input
          type="url"
          value={website}
          placeholder="https://example.com"
          onChange={(e) => setWebsite(e.currentTarget.value)}
          disabled={create.isPending}
        />
        <span className="text-xs font-normal text-muted-foreground">
          Optional, but a website gives us far more to work with.
        </span>
      </label>

      {error && (
        <p className="text-sm text-destructive" role="alert">
          {error}
        </p>
      )}

      <Button
        type="submit"
        className="self-start"
        disabled={create.isPending || name.trim() === ""}
      >
        <Sparkles data-icon="inline-start" />
        {create.isPending ? "Starting" : "Generate my setup"}
      </Button>
    </form>
  )
}

function ProposalFlow({
  businessId,
  promptLimit,
}: {
  businessId: string
  promptLimit: number
}) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const proposal = useProposal(businessId)
  const regen = useRegenProposal(businessId)

  const onApplied = async () => {
    await queryClient.invalidateQueries({ queryKey: ["me"] })
    navigate("/overview", { replace: true })
  }

  if (proposal.isError) {
    return (
      <ProgressState
        icon={<TriangleAlert className="text-destructive" />}
        title="Couldn't load your setup"
        description="Something went wrong fetching generation status."
        action={
          <Button type="button" onClick={() => void proposal.refetch()}>
            Try again
          </Button>
        }
      />
    )
  }
  if (proposal.isLoading || !proposal.data) {
    return <Skeleton className="h-64 w-full" />
  }

  const status = proposal.data.status

  const regenError =
    regen.error instanceof ApiError
      ? regen.error.message
      : regen.isError
        ? "Couldn't regenerate. Try again."
        : undefined

  if (status === "generating" || regen.isPending) {
    return <GenerationProgress stage={proposal.data?.stage} />
  }

  if (status === "failed") {
    // Manual setup: same review screen, empty (design 03, failure posture).
    return (
      <div className="flex flex-col gap-4">
        <div className="rounded-2xl border border-dashed p-5 text-sm text-muted-foreground">
          We couldn't generate a proposal automatically. You can fill in your
          setup manually below, or try generating again.
        </div>
        <ReviewScreen
          businessId={businessId}
          payload={EMPTY_PAYLOAD}
          promptLimit={promptLimit}
          onRegenerate={() => regen.mutate()}
          regenerating={regen.isPending}
          regenError={regenError}
          canRegenerate
          onApplied={onApplied}
        />
      </div>
    )
  }

  // status === "ready"
  return (
    <ReviewScreen
      businessId={businessId}
      payload={proposal.data.payload ?? EMPTY_PAYLOAD}
      promptLimit={promptLimit}
      onRegenerate={() => regen.mutate()}
      regenerating={regen.isPending}
      regenError={regenError}
      canRegenerate
      onApplied={onApplied}
    />
  )
}

// GENERATION_STEPS mirrors GenerateProfileWorkflow's stage order
// (internal/workflows/generate_profile.go). PersistProposal is folded into
// "drafting"; there is no terminal step because a ready proposal immediately
// swaps this screen for the review screen.
const GENERATION_STEPS: { stage: GenerationStage; label: string }[] = [
  { stage: "fetching_site", label: "Reading your website" },
  { stage: "researching", label: "Researching your business" },
  { stage: "drafting", label: "Drafting your profile and prompts" },
]

// GenerationProgress renders the live, stage-driven step list while the
// workflow generates. The current step comes from the polled stage; an absent
// stage (just-started run, pre-deploy workflow, or a degraded stage query)
// falls back to step 1. The only motion tied to progress is the real polled
// stage — completed steps get a checkmark that transitions in as the workflow
// advances.
function GenerationProgress({ stage }: { stage?: GenerationStage }) {
  const current = Math.max(
    0,
    GENERATION_STEPS.findIndex((s) => s.stage === stage)
  )
  return (
    <div className="flex flex-col items-center gap-6 rounded-3xl border border-dashed p-12 text-center">
      <div className="flex max-w-md flex-col gap-2">
        <h1 className="font-heading text-lg font-medium">Building your setup</h1>
        <p className="text-sm/relaxed text-muted-foreground">
          This usually takes a few minutes — you can leave this page and come
          back.
        </p>
      </div>
      <ol className="flex w-full max-w-xs flex-col gap-4 text-left">
        {GENERATION_STEPS.map((step, index) => {
          const done = index < current
          const active = index === current
          return (
            <li key={step.stage} className="flex items-center gap-3">
              <span
                className={`flex size-6 shrink-0 items-center justify-center rounded-full transition-colors [&_svg]:size-3.5 ${
                  done
                    ? "bg-primary text-primary-foreground"
                    : active
                      ? "text-primary"
                      : "text-muted-foreground"
                }`}
              >
                {done ? (
                  <Check strokeWidth={3} />
                ) : active ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <span className="size-2 rounded-full bg-current opacity-40" />
                )}
              </span>
              <span
                className={`text-sm transition-colors ${
                  active
                    ? "font-medium text-foreground"
                    : done
                      ? "text-foreground"
                      : "text-muted-foreground"
                }`}
              >
                {step.label}
              </span>
            </li>
          )
        })}
      </ol>
    </div>
  )
}

function ProgressState({
  icon,
  title,
  description,
  action,
}: {
  icon: ReactNode
  title: string
  description: string
  action?: ReactNode
}) {
  return (
    <div className="flex flex-col items-center gap-4 rounded-3xl border border-dashed p-12 text-center">
      <div className="flex size-12 items-center justify-center rounded-2xl bg-muted [&_svg]:size-6">
        {icon}
      </div>
      <div className="flex max-w-md flex-col gap-2">
        <h1 className="font-heading text-lg font-medium">{title}</h1>
        <p className="text-sm/relaxed text-muted-foreground">{description}</p>
      </div>
      {action}
    </div>
  )
}

function OnboardingShell({
  children,
  wide,
}: {
  children: ReactNode
  wide?: boolean
}) {
  return (
    <main className="flex min-h-svh flex-col items-center bg-background p-6">
      <div className="flex w-full items-center gap-2 py-2">
        <Building2 className="size-5" />
        <span className="font-heading text-sm font-medium">OpenSight</span>
      </div>
      <div
        className={`mt-6 w-full ${wide ? "max-w-3xl" : "max-w-md"} flex flex-col`}
      >
        {children}
      </div>
    </main>
  )
}
