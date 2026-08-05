import { Navigate, useSearchParams } from "react-router"

import { useMe } from "@/api/hooks"
import { Button } from "@/components/ui/button"
import { Logo } from "@/components/logo"

// Google is the only sign-in method (design 07 "Auth and accounts"): there is
// no password to enter, so this is a link, not a form. /auth/google/start is
// a plain HTTP route on the Go server (google_auth.go), not a Connect RPC —
// a full-page navigation, not a fetch.
export function LoginPage() {
  const me = useMe()
  const [searchParams] = useSearchParams()

  if (me.data) {
    return <Navigate to="/overview" replace />
  }

  const error = searchParams.get("error")
    ? "Could not sign in with Google. Try again."
    : undefined

  return (
    <main className="flex min-h-svh items-center justify-center bg-background p-6">
      <div className="flex w-full max-w-sm flex-col gap-5 rounded-lg border bg-card p-6 shadow-sm">
        <div className="flex flex-col gap-1">
          <div className="flex items-center gap-2">
            <Logo className="size-5" />
            <h1 className="font-heading text-xl font-semibold">OpenSight</h1>
          </div>
          <p className="text-sm text-muted-foreground">
            Sign in to continue.
          </p>
        </div>

        {error && (
          <p className="text-sm text-destructive" role="alert">
            {error}
          </p>
        )}

        <Button render={<a href="/auth/google/start" />}>
          <GoogleIcon data-icon="inline-start" />
          Continue with Google
        </Button>
      </div>
    </main>
  )
}

function GoogleIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 18 18" aria-hidden="true" {...props}>
      <path
        fill="#4285F4"
        d="M17.64 9.2c0-.64-.06-1.25-.16-1.84H9v3.48h4.84a4.14 4.14 0 0 1-1.8 2.72v2.26h2.9c1.7-1.57 2.68-3.87 2.68-6.62Z"
      />
      <path
        fill="#34A853"
        d="M9 18c2.43 0 4.47-.8 5.96-2.18l-2.9-2.26c-.8.54-1.84.86-3.06.86-2.35 0-4.34-1.59-5.05-3.72H.9v2.33A9 9 0 0 0 9 18Z"
      />
      <path
        fill="#FBBC05"
        d="M3.95 10.7A5.4 5.4 0 0 1 3.67 9c0-.59.1-1.17.28-1.7V4.97H.9A9 9 0 0 0 0 9c0 1.45.35 2.83.9 4.03l3.05-2.33Z"
      />
      <path
        fill="#EA4335"
        d="M9 3.58c1.32 0 2.5.46 3.44 1.35l2.58-2.58C13.46.89 11.43 0 9 0A9 9 0 0 0 .9 4.97l3.05 2.33C4.66 5.17 6.65 3.58 9 3.58Z"
      />
    </svg>
  )
}
