// Onboarding flow (ONB-5, design 03): create form → progress state polling the
// generation proposal → review screen with every value editable. A failed
// generation drops into the same review screen, empty, for manual setup. The
// draft is recoverable via /me, so a reload mid-flow resumes rather than
// restarting.
import { create } from "@bufbuild/protobuf"
import {
  createConnectQueryKey,
  useMutation,
  useQuery,
} from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { type FormEvent, type ReactNode, useState } from "react"
import {
  Building2,
  Check,
  LoaderCircle,
  Sparkles,
  TriangleAlert,
} from "lucide-react"
import { Navigate, useNavigate } from "react-router"

import { errorMessage, isUnauthenticated } from "@/api/errors"
import { useBillingAccess, useMe, usePlan } from "@/api/hooks"
import { BusinessStatus, GenerationStage, ProposalStatus } from "@/gen/opensight/v1/common_pb"
import { ProposalPayloadSchema, type ProposalPayload } from "@/gen/opensight/v1/business_pb"
import {
  createBusiness,
  getProposal,
  regenerateProposal,
} from "@/gen/opensight/v1/business-BusinessService_connectquery"
import { getMe } from "@/gen/opensight/v1/auth-AuthService_connectquery"
import { ReviewScreen } from "@/pages/onboarding/review-screen"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"

const EMPTY_PAYLOAD: ProposalPayload = create(ProposalPayloadSchema, {
  lowConfidence: false,
  profile: {
    name: "",
    aliases: [],
    category: "",
    services: [],
    location: { address: "", area: "", city: "", country: "" },
  },
  prompts: [],
  sources: [],
})

export function OnboardingPage() {
  const me = useMe()
  const plan = usePlan()
  const { isActive } = useBillingAccess()
  const [createdId, setCreatedId] = useState<string>()

  if (me.isLoading) {
    return (
      <OnboardingShell>{<Skeleton className="h-64 w-full" />}</OnboardingShell>
    )
  }
  if (isUnauthenticated(me.error)) {
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
  // Onboarding renders outside AppLayout, so it needs its own copy of this
  // redirect (BILL-9). Onboarding is entirely classActive (BILL-10 — it
  // creates a business and generates a proposal, both LLM spend), so a
  // lapsed tenant that never finished onboarding has nowhere else useful to
  // go either — not just a never-paid one.
  if (!isActive) {
    return <Navigate to="/billing" replace />
  }

  const businesses = me.data.businesses
  const draft = businesses.find((b) => b.status === BusinessStatus.DRAFT)
  // MVP is one business per tenant: an already-active business means onboarding
  // is done, so send the user into the app rather than letting them start over.
  const active = businesses.find((b) => b.status !== BusinessStatus.DRAFT)
  const businessId = createdId ?? draft?.id

  if (!businessId) {
    if (active) return <Navigate to="/overview" replace />
    return (
      <OnboardingShell>
        <CreateForm onCreated={setCreatedId} />
      </OnboardingShell>
    )
  }

  // plan.plan can be momentarily undefined even once me.data has loaded (the
  // same GetMe response, one field). Guard rather than fall back to a bogus
  // default limit like 0, which would silently allow "add exactly 0
  // questions" (BILL-6).
  if (!plan.plan) {
    return (
      <OnboardingShell wide>
        <Skeleton className="h-64 w-full" />
      </OnboardingShell>
    )
  }

  return (
    <OnboardingShell wide>
      <ProposalFlow
        businessId={businessId}
        promptLimit={plan.plan.promptLimit}
      />
    </OnboardingShell>
  )
}

function CreateForm({ onCreated }: { onCreated: (id: string) => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState("")
  const [website, setWebsite] = useState("")

  const create = useMutation(createBusiness, {
    onSuccess: async (data) => {
      // Refresh /me so the draft is resumable on reload, then enter the flow.
      await queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({ schema: getMe, cardinality: "finite" }),
      })
      if (data.business) onCreated(data.business.id)
    },
  })

  const error = create.isError
    ? errorMessage(create.error, "Could not start setup. Try again.")
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
  // useProposal polled while generation is running; a ready or failed
  // proposal is terminal, so polling stops (matches ResultService.ListRuns'
  // data-driven refetchInterval elsewhere).
  const proposal = useQuery(
    getProposal,
    { businessId },
    {
      refetchInterval: (query) =>
        query.state.data?.state?.status === ProposalStatus.GENERATING
          ? 5000
          : false,
    }
  )
  const regen = useMutation(regenerateProposal, {
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: getProposal,
          input: { businessId },
          cardinality: "finite",
        }),
      })
    },
  })

  const onApplied = async () => {
    await queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({ schema: getMe, cardinality: "finite" }),
    })
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
  if (proposal.isLoading || !proposal.data?.state) {
    return <Skeleton className="h-64 w-full" />
  }

  const status = proposal.data.state.status

  const regenError = regen.isError
    ? errorMessage(regen.error, "Couldn't regenerate. Try again.")
    : undefined

  if (status === ProposalStatus.GENERATING || regen.isPending) {
    return <GenerationProgress stage={proposal.data.state.stage} />
  }

  if (status === ProposalStatus.FAILED) {
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
          onRegenerate={() => regen.mutate({ businessId })}
          regenerating={regen.isPending}
          regenError={regenError}
          canRegenerate
          onApplied={onApplied}
        />
      </div>
    )
  }

  // status === ProposalStatus.READY
  return (
    <ReviewScreen
      businessId={businessId}
      payload={proposal.data.state.payload ?? EMPTY_PAYLOAD}
      promptLimit={promptLimit}
      onRegenerate={() => regen.mutate({ businessId })}
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
  { stage: GenerationStage.FETCHING_SITE, label: "Reading your website" },
  { stage: GenerationStage.DRAFTING, label: "Researching and drafting your profile" },
]

// GenerationProgress renders the live, stage-driven step list while the
// workflow generates. The current step comes from the polled stage; an absent
// stage (just-started run, pre-deploy workflow, or a degraded stage query)
// falls back to step 1. The only motion tied to progress is the real polled
// stage — completed steps get a checkmark that transitions in as the workflow
// advances.
function GenerationProgress({ stage }: { stage: GenerationStage }) {
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
