import { Navigate, Route, Routes, useSearchParams } from "react-router"

import { AppLayout } from "@/components/app-layout"
import { BillingPage } from "@/pages/billing/billing-page"
import { CheckoutReturnPage } from "@/pages/checkout/checkout-return-page"
import { CompetitorsPage } from "@/pages/competitors/competitors-page"
import { LoginPage } from "@/pages/login/login-page"
import { OnboardingPage } from "@/pages/onboarding/onboarding-page"
import { OverviewPage } from "@/pages/overview/overview-page"
import { MethodologyPage } from "@/pages/methodology/methodology-page"
import { PrivacyPage } from "@/pages/privacy/privacy-page"
import { PromptDetailPage } from "@/pages/prompts/prompt-detail-page"
import { PromptsPage } from "@/pages/prompts/prompts-page"
import { RunDetailPage } from "@/pages/runs/run-detail-page"
import { RunsPage } from "@/pages/runs/runs-page"
import { SetupPage } from "@/pages/setup/setup-page"
import { AccountsPage, NewAccountPage } from "@/pages/accounts/accounts-page"
import { TeamPage } from "@/pages/team/team-page"
import { OpportunitiesPage } from "@/pages/opportunities/opportunities-page"
import { useParams } from "react-router"
import { accountPath } from "@/lib/account-path"

// ResponsesRedirect keeps the old /responses(?run=X) deep links working after
// the RUNS-6 rename: a bare wildcard route would drop ?run=, so this reads it
// explicitly and forwards the run-scoped filters (status/prompt) along.
function ResponsesRedirect() {
  const [searchParams] = useSearchParams()
  const { accountSlug = "" } = useParams<{ accountSlug: string }>()
  const run = searchParams.get("run")
  if (!run) return <Navigate to={accountPath(accountSlug, "/runs")} replace />
  const params = new URLSearchParams(searchParams)
  params.delete("run")
  const query = params.toString()
  return (
    <Navigate
      to={`${accountPath(accountSlug, `/runs/${run}`)}${query ? `?${query}` : ""}`}
      replace
    />
  )
}

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      {/* Google is the only sign-in method (design 07): signup is login. The
          marketing site links here from four pages. */}
      <Route path="/signup" element={<Navigate to="/login" replace />} />
      <Route path="/accounts" element={<AccountsPage />} />
      <Route path="/accounts/new" element={<NewAccountPage />} />
      <Route path="/checkout/return" element={<CheckoutReturnPage />} />
      <Route path="/a/:accountSlug/onboarding" element={<OnboardingPage />} />
      <Route path="/a/:accountSlug" element={<AppLayout />}>
        <Route index element={<Navigate to="overview" replace />} />
        <Route path="overview" element={<OverviewPage />} />
        <Route path="prompts" element={<PromptsPage />} />
        <Route path="prompts/:id" element={<PromptDetailPage />} />
        <Route path="competitors" element={<CompetitorsPage />} />
        <Route path="opportunities" element={<OpportunitiesPage />} />
        <Route path="runs" element={<RunsPage />} />
        <Route path="runs/:id" element={<RunDetailPage />} />
        <Route path="responses" element={<ResponsesRedirect />} />
        <Route path="setup" element={<SetupPage />} />
        <Route path="team" element={<TeamPage />} />
        <Route path="billing" element={<BillingPage />} />
        <Route path="methodology" element={<MethodologyPage />} />
        <Route path="privacy" element={<PrivacyPage />} />
        <Route path="*" element={<Navigate to="overview" replace />} />
      </Route>
      <Route path="*" element={<Navigate to="/accounts" replace />} />
    </Routes>
  )
}

export default App
