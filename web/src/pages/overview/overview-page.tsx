import { LayoutDashboard } from "lucide-react"

import { SectionPlaceholder } from "@/components/section-placeholder"

export function OverviewPage() {
  return (
    <SectionPlaceholder
      title="Overview"
      description="Visibility headline, trend, and top panels arrive in Phase 2."
      icon={LayoutDashboard}
    />
  )
}
