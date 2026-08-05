// Persistent lapsed banner (BILL-10, design 08 "Lapse and reactivation"):
// generic and non-dated on purpose — the exact lapse/renewal dates already
// live one click away on /billing, so nothing here is threaded through the
// session payload.
import { Link } from "react-router"

import { useAccountContext, useBillingAccess } from "@/api/hooks"
import { AccountRole } from "@/gen/opensight/v1/account_pb"
import { accountPath } from "@/lib/account-path"
import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from "@/components/ui/alert"
import { Button } from "@/components/ui/button"

export function BillingBanner() {
  const { isLapsed } = useBillingAccess()
  const { data: account } = useAccountContext()
  if (!isLapsed) return null

  return (
    <Alert className="rounded-none border-x-0 border-t-0">
      <AlertTitle>Monitoring has stopped.</AlertTitle>
      <AlertDescription>
        Your questions, competitors and every collected response stay
        readable, and you can keep editing them.
      </AlertDescription>
      {account?.account && account.role === AccountRole.OWNER && <AlertAction>
        <Button size="sm" render={<Link to={accountPath(account.account.slug, "/billing")} />}>
          Reactivate
        </Button>
      </AlertAction>}
    </Alert>
  )
}
