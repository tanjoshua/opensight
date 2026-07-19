import { Settings } from "lucide-react"

import { SectionPlaceholder } from "@/components/section-placeholder"

export function SetupPage() {
  return (
    <SectionPlaceholder
      title="Setup"
      description="Profile, prompt management, and plan display arrive in Phase 3."
      icon={Settings}
    />
  )
}
