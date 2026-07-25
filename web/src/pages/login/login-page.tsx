import { type FormEvent, useState } from "react"
import { useMutation, createConnectQueryKey } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { LogIn } from "lucide-react"
import { Navigate, useNavigate } from "react-router"

import { errorMessage } from "@/api/errors"
import { useMe } from "@/api/hooks"
import { getMe, login } from "@/gen/opensight/v1/auth-AuthService_connectquery"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

export function LoginPage() {
  const me = useMe()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")

  const loginMutation = useMutation(login, {
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({ schema: getMe, cardinality: "finite" }),
      })
      navigate("/responses", { replace: true })
    },
  })

  if (me.data) {
    return <Navigate to="/responses" replace />
  }

  const error = loginMutation.isError
    ? errorMessage(loginMutation.error, "Login failed. Try again.")
    : undefined

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    loginMutation.mutate({ email, password })
  }

  return (
    <main className="flex min-h-svh items-center justify-center bg-background p-6">
      <form
        className="flex w-full max-w-sm flex-col gap-5 rounded-lg border bg-card p-6 shadow-sm"
        onSubmit={onSubmit}
      >
        <div className="flex flex-col gap-1">
          <h1 className="font-heading text-xl font-semibold">OpenSight</h1>
          <p className="text-sm text-muted-foreground">Log in to continue.</p>
        </div>

        <div className="flex flex-col gap-3">
          <label className="flex flex-col gap-1.5 text-sm font-medium">
            Email
            <Input
              type="email"
              autoComplete="email"
              value={email}
              onChange={(event) => setEmail(event.currentTarget.value)}
              disabled={loginMutation.isPending}
              aria-invalid={loginMutation.isError}
              required
            />
          </label>
          <label className="flex flex-col gap-1.5 text-sm font-medium">
            Password
            <Input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(event) => setPassword(event.currentTarget.value)}
              disabled={loginMutation.isPending}
              aria-invalid={loginMutation.isError}
              required
            />
          </label>
        </div>

        {error && (
          <p className="text-sm text-destructive" role="alert">
            {error}
          </p>
        )}

        <Button type="submit" disabled={loginMutation.isPending}>
          <LogIn data-icon="inline-start" />
          {loginMutation.isPending ? "Logging in" : "Log in"}
        </Button>
      </form>
    </main>
  )
}
