import { ArrowLeft } from "lucide-react"
import { Link, useNavigate } from "react-router"

import { Button } from "@/components/ui/button"

export function MethodologyPage() {
  const navigate = useNavigate()

  return (
    <div className="flex w-full flex-col gap-4">
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label="Back"
        onClick={() => navigate(-1)}
      >
        <ArrowLeft />
      </Button>

      <article className="mx-auto flex w-full max-w-3xl flex-col gap-8">
        <header className="flex flex-col gap-2">
          <h1 className="font-heading text-2xl font-semibold">
            How we measure
          </h1>
          <p className="text-muted-foreground">
            OpenSight measures a consistent API-based sample of AI answers. It
            is useful evidence, not a capture of every consumer experience.
          </p>
        </header>

        <MethodSection title="How responses are produced">
          <p>
            Monitoring prompts run through the OpenAI Responses API with web
            search. We include the business location as context so
            geographically specific recommendations can be evaluated.
          </p>
          <p>
            This is a proxy for consumer ChatGPT, not a recording of
            chatgpt.com. API requests have no conversation history, memory, or
            personalisation, and model routing can differ from the consumer
            product. The model reported by the API is stored with each response
            and shown in its response drawer.
          </p>
        </MethodSection>

        <MethodSection title="How visibility is calculated">
          <p>
            Visibility is the share of analyzed, valid responses that mention
            the business:
          </p>
          <p className="rounded-md border bg-muted/30 p-4 font-medium">
            responses mentioning the business ÷ all analyzed valid responses
          </p>
          <p>
            Failed responses and responses that have not yet been analyzed are
            excluded from both sides of the calculation.
          </p>
        </MethodSection>

        <MethodSection title="Evidence and limits">
          <p>
            Every displayed metric links back to the stored responses behind it,
            so you can inspect the answer, prompt, citations, analysis, model,
            and run metadata.
          </p>
          <p>
            These measurements do not guarantee factual accuracy, consumer
            behaviour, or future visibility. AI answers and web results can
            change between runs.
          </p>
        </MethodSection>

        <p className="border-t pt-6 text-sm text-muted-foreground">
          For how account and monitoring data is handled, read our{" "}
          <Link
            className="font-medium text-foreground underline underline-offset-4"
            to="/privacy"
          >
            privacy overview
          </Link>
          .
        </p>
      </article>
    </div>
  )
}

function MethodSection({
  title,
  children,
}: {
  title: string
  children: React.ReactNode
}) {
  return (
    <section className="flex flex-col gap-3">
      <h2 className="font-heading text-lg font-semibold">{title}</h2>
      <div className="flex flex-col gap-3 text-sm leading-6">{children}</div>
    </section>
  )
}
