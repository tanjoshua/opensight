import { Link } from "react-router"

export function PrivacyPage() {
  return (
    <article className="mx-auto flex w-full max-w-3xl flex-col gap-8">
      <header className="flex flex-col gap-2">
        <h1 className="font-heading text-2xl font-semibold">Privacy overview</h1>
        <p className="text-muted-foreground">
          A plain-language summary of how OpenSight handles data. It is informed
          by Singapore PDPA considerations, but is not a claim of legal
          compliance or legal advice.
        </p>
      </header>

      <PrivacySection title="What we store">
        <ul className="list-disc space-y-2 pl-5">
          <li>
            Account data, including user email addresses, password verifiers,
            and active session records.
          </li>
          <li>
            Business profiles, including names, websites, aliases, category,
            services, location, and public practitioner names and roles.
          </li>
          <li>Monitoring prompts and their replacement history.</li>
          <li>
            Raw monitoring responses, citations, model and timing metadata,
            statuses, and errors.
          </li>
          <li>
            Derived analysis such as mentions, position, sentiment, themes,
            competitor observations, citation sources, and aggregate metrics.
          </li>
          <li>
            Service configuration and operations data, including plan
            entitlements, schedules, runs, usage, and cost records.
          </li>
        </ul>
      </PrivacySection>

      <PrivacySection title="No patient-identifiable data">
        <p>
          OpenSight is only for generic consumer discovery monitoring. Users
          must not enter patient names, contact details, case notes, health
          information, or any other patient-identifiable data.
        </p>
        <p>
          Prompt guidance and review copy reinforce this boundary, but we do not
          claim that the product automatically detects or prevents every
          prohibited entry.
        </p>
      </PrivacySection>

      <PrivacySection title="OpenAI processing">
        <p>
          Monitoring prompts and relevant business context are sent to the
          OpenAI API, and web search may be used to produce responses. OpenAI&apos;s
          handling depends on the service agreement and settings applicable to
          the deployed account; OpenSight does not make a broader promise here
          about OpenAI&apos;s retention or model-training practices.
        </p>
      </PrivacySection>

      <PrivacySection title="Hosting and retention">
        <p>
          OpenSight is currently pre-production. Singapore hosting is planned
          before production launch, but no production hosting region is claimed
          until that infrastructure work is completed and verified.
        </p>
        <p>
          Monitoring results are retained indefinitely to preserve trends and
          historical evidence, unless a later contract or policy sets a
          different period. Legal, security, or dispute-preservation needs may
          override ordinary deletion.
        </p>
        <p>
          Account and business-profile data is retained as needed to operate the
          service and manage the customer relationship. Specific deletion or
          post-account terms should be confirmed with the account
          representative rather than inferred from this overview.
        </p>
      </PrivacySection>

      <PrivacySection title="Questions or requests">
        <p>
          Contact your OpenSight account representative about access,
          correction, deletion, retention, or data-handling questions.
        </p>
      </PrivacySection>

      <p className="border-t pt-6 text-sm text-muted-foreground">
        For how monitoring results and metrics are produced, read{" "}
        <Link
          className="font-medium text-foreground underline underline-offset-4"
          to="/methodology"
        >
          How we measure
        </Link>
        .
      </p>
    </article>
  )
}

function PrivacySection({
  title,
  children,
}: {
  title: string
  children: React.ReactNode
}) {
  return (
    <section className="flex flex-col gap-3 text-sm leading-6">
      <h2 className="font-heading text-lg font-semibold">{title}</h2>
      {children}
    </section>
  )
}
