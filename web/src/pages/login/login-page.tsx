import { LogIn } from "lucide-react"

import { SectionPlaceholder } from "@/components/section-placeholder"

// Bare placeholder route; the login form arrives with AUTH-4.
export function LoginPage() {
  return (
    <div className="flex min-h-svh flex-col p-6">
      <SectionPlaceholder
        title="Log in"
        description="The login form arrives with AUTH-4."
        icon={LogIn}
      />
    </div>
  )
}
