import { useMutation } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { ArrowRight, LogOut, Plus } from "lucide-react"
import { useState, type FormEvent } from "react"
import { Link, Navigate, useNavigate } from "react-router"

import { errorMessage, isUnauthenticated } from "@/api/errors"
import { useMe } from "@/api/hooks"
import { logout } from "@/gen/opensight/v1/auth-AuthService_connectquery"
import { createAccount } from "@/gen/opensight/v1/account-AccountService_connectquery"
import { accountPath } from "@/lib/account-path"
import { isCompedAccountAdmin } from "@/lib/operator"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Logo } from "@/components/logo"

export function AccountsPage() {
  const me = useMe()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const logoutMutation = useMutation(logout, {
    onSettled: () => {
      queryClient.clear()
      navigate("/login", { replace: true })
    },
  })

  if (me.isLoading)
    return (
      <AccountShell>
        <p className="text-sm text-muted-foreground">Loading workspaces…</p>
      </AccountShell>
    )
  if (isUnauthenticated(me.error)) return <Navigate to="/login" replace />
  if (!me.data || me.isError)
    return (
      <AccountShell>
        <p role="alert" className="text-sm text-destructive">
          Your workspaces could not be loaded.
        </p>
      </AccountShell>
    )
  if (me.data.memberships.length === 1 && me.data.memberships[0].account) {
    return (
      <Navigate to={accountPath(me.data.memberships[0].account.slug)} replace />
    )
  }

  return (
    <AccountShell>
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="font-heading text-2xl font-semibold">
            Choose a workspace
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            A workspace keeps its businesses, members, and billing together.
          </p>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Log out"
          onClick={() => logoutMutation.mutate({})}
        >
          <LogOut />
        </Button>
      </div>
      {isCompedAccountAdmin(me.data.user?.email) && (
        <Button className="self-start" render={<Link to="/accounts/new" />}>
          <Plus />
          Create comped workspace
        </Button>
      )}
      <div className="grid gap-3">
        {me.data.memberships.map(
          (membership) =>
            membership.account && (
              <Link
                key={membership.account.id}
                to={accountPath(membership.account.slug)}
                className="group rounded-lg outline-none focus-visible:ring-3 focus-visible:ring-ring/30"
              >
                <Card className="transition-colors group-hover:bg-muted/40">
                  <CardHeader className="flex-row items-center justify-between gap-3">
                    <div className="min-w-0">
                      <CardTitle className="truncate">
                        {membership.account.name}
                      </CardTitle>
                      <CardDescription>Open workspace</CardDescription>
                    </div>
                    <div className="flex items-center gap-2">
                      <Badge variant="outline">
                        {roleLabel(membership.role)}
                      </Badge>
                      <ArrowRight className="size-4 text-muted-foreground" />
                    </div>
                  </CardHeader>
                </Card>
              </Link>
            )
        )}
      </div>
    </AccountShell>
  )
}

export function NewAccountPage() {
  const me = useMe()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [name, setName] = useState("")
  const isAdmin = isCompedAccountAdmin(me.data?.user?.email)
  const mutation = useMutation(createAccount, {
    onSuccess: async ({ membership }) => {
      await queryClient.invalidateQueries()
      if (membership?.account)
        navigate(
          accountPath(
            membership.account.slug,
            isAdmin ? "/onboarding" : "/billing"
          ),
          {
            replace: true,
          }
        )
    },
  })

  if (me.isLoading)
    return (
      <AccountShell>
        <p className="text-sm text-muted-foreground">Loading…</p>
      </AccountShell>
    )
  if (isUnauthenticated(me.error)) return <Navigate to="/login" replace />
  if (!me.data || me.isError)
    return (
      <AccountShell>
        <p role="alert" className="text-sm text-destructive">
          Your account could not be loaded.
        </p>
      </AccountShell>
    )

  function submit(event: FormEvent) {
    event.preventDefault()
    const trimmed = name.trim()
    if (trimmed) mutation.mutate({ name: trimmed })
  }

  return (
    <AccountShell>
      <Card>
        <CardHeader>
          <CardTitle>Create a workspace</CardTitle>
          <CardDescription>
            {isAdmin
              ? "This workspace will have complimentary access. You can add members later."
              : "Workspaces keep member access, businesses, and billing separate."}
          </CardDescription>
        </CardHeader>
        <form onSubmit={submit}>
          <CardContent>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="account-name">Workspace name</FieldLabel>
                <Input
                  id="account-name"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  autoFocus
                  required
                  maxLength={120}
                  placeholder="Acme"
                />
                {mutation.isError && (
                  <FieldError>
                    {errorMessage(
                      mutation.error,
                      "Couldn't create the workspace."
                    )}
                  </FieldError>
                )}
              </Field>
            </FieldGroup>
          </CardContent>
          <CardFooter className="justify-between">
            <Button
              variant="ghost"
              type="button"
              render={<Link to="/accounts" />}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={!name.trim() || mutation.isPending}>
              {mutation.isPending
                ? "Creating…"
                : isAdmin
                  ? "Create comped workspace"
                  : "Create workspace"}
            </Button>
          </CardFooter>
        </form>
      </Card>
    </AccountShell>
  )
}

function AccountShell({ children }: { children: React.ReactNode }) {
  return (
    <main className="min-h-svh bg-muted/30 p-6">
      <div className="mx-auto flex w-full max-w-xl flex-col gap-5">
        <Link to="/accounts" className="flex items-center gap-2 self-start">
          <Logo className="size-5" />
          <span className="font-heading font-semibold">OpenSight</span>
        </Link>
        {children}
      </div>
    </main>
  )
}

function roleLabel(role: number) {
  return (
    (
      { 1: "Owner", 2: "Admin", 3: "Member", 4: "Viewer" } as Record<
        number,
        string
      >
    )[role] ?? "Member"
  )
}
