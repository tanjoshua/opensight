import { type FormEvent, useState } from "react"
import { useMutation, createConnectQueryKey } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { UserPlus } from "lucide-react"
import { Link, Navigate, useNavigate } from "react-router"

import { errorMessage } from "@/api/errors"
import { useMe } from "@/api/hooks"
import { getMe, signup } from "@/gen/opensight/v1/auth-AuthService_connectquery"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

// Near-copy of login-page.tsx (design 08 "The funnel" — signup and login are
// one shell). A successful signup lands authenticated and unpaid, so it
// forwards to /billing rather than /overview (BILL-3, BILL-9).
export function SignupPage() {
  const me = useMe()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")

  const signupMutation = useMutation(signup, {
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: getMe,
          cardinality: "finite",
        }),
      })
      navigate("/billing", { replace: true })
    },
  })

  if (me.data) {
    return <Navigate to="/overview" replace />
  }

  const error = signupMutation.isError
    ? errorMessage(
        signupMutation.error,
        "Could not create your account. Try again."
      )
    : undefined

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    signupMutation.mutate({ email, password })
  }

  return (
    <main className="flex min-h-svh items-center justify-center bg-background p-6">
      <form
        className="flex w-full max-w-sm flex-col gap-5 rounded-lg border bg-card p-6 shadow-sm"
        onSubmit={onSubmit}
      >
        <div className="flex flex-col gap-1">
          <h1 className="font-heading text-xl font-semibold">OpenSight</h1>
          <p className="text-sm text-muted-foreground">
            Create an account to get started.
          </p>
        </div>

        <div className="flex flex-col gap-3">
          <label className="flex flex-col gap-1.5 text-sm font-medium">
            Email
            <Input
              type="email"
              autoComplete="email"
              value={email}
              onChange={(event) => setEmail(event.currentTarget.value)}
              disabled={signupMutation.isPending}
              aria-invalid={signupMutation.isError}
              required
            />
          </label>
          <label className="flex flex-col gap-1.5 text-sm font-medium">
            Password
            <Input
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(event) => setPassword(event.currentTarget.value)}
              disabled={signupMutation.isPending}
              aria-invalid={signupMutation.isError}
              required
            />
          </label>
        </div>

        {error && (
          <p className="text-sm text-destructive" role="alert">
            {error}
          </p>
        )}

        <Button type="submit" disabled={signupMutation.isPending}>
          <UserPlus data-icon="inline-start" />
          {signupMutation.isPending ? "Creating account" : "Create account"}
        </Button>

        <p className="text-center text-sm text-muted-foreground">
          Already have an account?{" "}
          <Link
            className="font-medium text-foreground hover:underline"
            to="/login"
          >
            Log in
          </Link>
        </p>
      </form>
    </main>
  )
}
