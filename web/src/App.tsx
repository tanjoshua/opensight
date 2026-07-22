import { Navigate, Route, Routes } from "react-router"

import { AppLayout } from "@/components/app-layout"
import { CompetitorsPage } from "@/pages/competitors/competitors-page"
import { LoginPage } from "@/pages/login/login-page"
import { OnboardingPage } from "@/pages/onboarding/onboarding-page"
import { OverviewPage } from "@/pages/overview/overview-page"
import { PromptDetailPage } from "@/pages/prompts/prompt-detail-page"
import { PromptsPage } from "@/pages/prompts/prompts-page"
import { ResponsesPage } from "@/pages/responses/responses-page"
import { SetupPage } from "@/pages/setup/setup-page"

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/onboarding" element={<OnboardingPage />} />
      <Route element={<AppLayout />}>
        <Route index element={<Navigate to="/responses" replace />} />
        <Route path="/overview" element={<OverviewPage />} />
        <Route path="/prompts" element={<PromptsPage />} />
        <Route path="/prompts/:id" element={<PromptDetailPage />} />
        <Route path="/competitors" element={<CompetitorsPage />} />
        <Route path="/responses" element={<ResponsesPage />} />
        <Route path="/setup" element={<SetupPage />} />
        <Route path="*" element={<Navigate to="/responses" replace />} />
      </Route>
    </Routes>
  )
}

export default App
