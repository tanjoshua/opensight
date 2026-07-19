import { Sparkles } from "lucide-react"

import { SectionPlaceholder } from "@/components/section-placeholder"

// Bare placeholder route; the onboarding draft flow arrives in Phase 3.
export function OnboardingPage() {
  return (
    <div className="flex min-h-svh flex-col p-6">
      <SectionPlaceholder
        title="Onboarding"
        description="The guided onboarding flow arrives in Phase 3."
        icon={Sparkles}
      />
    </div>
  )
}
