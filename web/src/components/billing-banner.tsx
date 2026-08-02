// Persistent lapsed banner (BILL-10, design 08 "Lapse and reactivation"):
// generic and non-dated on purpose — the exact lapse/renewal dates already
// live one click away on /billing, so nothing here is threaded through the
// session payload.
import { Link } from "react-router"

import { useBillingAccess } from "@/api/hooks"
import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from "@/components/ui/alert"
import { Button } from "@/components/ui/button"

export function BillingBanner() {
  const { isLapsed } = useBillingAccess()
  if (!isLapsed) return null

  return (
    <Alert className="rounded-none border-x-0 border-t-0">
      <AlertTitle>Monitoring has stopped.</AlertTitle>
      <AlertDescription>
        Your questions, competitors and every collected response stay
        readable, and you can keep editing them.
      </AlertDescription>
      <AlertAction>
        <Button size="sm" render={<Link to="/billing" />}>
          Reactivate
        </Button>
      </AlertAction>
    </Alert>
  )
}
