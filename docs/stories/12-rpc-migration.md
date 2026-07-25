# Epic 12 — Protobuf/Connect RPC migration (RPC)

Replace the hand-written REST/JSON contract (`internal/api` chi handlers, `web/src/api` TS client)
with `.proto`-defined services served over Connect RPC, generating both the Go server stubs and the
TypeScript client so the contract is machine-checked instead of hand-mirrored. Full cutover, one
branch — no external API consumers, so REST and Connect never coexist for long. Phase 3 (post-MVP
infra work).

---

## RPC-1 — Codegen pipeline

As the developer, I want `buf` wired into the build so `.proto` files generate committed Go and
TypeScript code, so that the contract has one source of truth.

- [x] `buf.yaml` (v2, module at `proto/`) and `buf.gen.yaml` (managed mode, four local plugins:
      `protoc-gen-go`, `protoc-gen-connect-go` as `go tool` entries; `protoc-gen-es`,
      `protoc-gen-connect-query` from `web/node_modules/.bin`). `protoc-gen-es` also carries
      `erasable_syntax=true`, since `web/tsconfig.app.json`'s `erasableSyntaxOnly` rejects the
      plain TS `enum` it would otherwise emit — caught before RPC-2 adds real enums.
- [x] `go.mod` gains `connectrpc.com/connect` pinned via the `tool` block (stays `// indirect`
      until a real Connect service imports it — `go mod tidy` reproduces this deterministically,
      confirmed) and `tool` directives for the two Go plugins; `web/package.json` gains
      `@bufbuild/buf`, `@bufbuild/protobuf`, `@connectrpc/connect`, `connect-web`, `connect-query`,
      plus the two protoc-gen devDependencies.
- [x] `make proto` runs `buf format -w && buf lint && buf generate`.
- [x] CI `test` job installs Node, runs `make proto`, and fails on `git diff --exit-code` if
      generated output is stale.
- [x] `internal/gen` excluded from `.golangci.yml`; `web/src/gen` excluded from `web/eslint.config.js`
      and a new `web/.prettierignore`.
- [x] `go build ./...`, `go test ./...`, and `npm run build` are unaffected before any proto file
      exists (empty `proto/` module is valid).

Deps: — · Phase 3 · Ref: design 06 (API conventions)

## RPC-2 — Proto schema for the full surface

As the developer, I want every endpoint's request/response shape defined in `.proto`, so that the
schema is complete and reviewable before any server code changes.

- [x] `proto/opensight/v1/{common,auth,business,overview,citation,prompt,competitor,result}.proto`
      covering all 25 existing `/api/v1` endpoints (26 router entries counting `/healthz`, which is
      not an RPC) across 7 services, 23 RPCs — `SetCompetitorStatus` and `ReviewSuggestedAlias` each
      merge a track/dismiss or approve/reject pair, matching the single Go handler function and
      single frontend call each pair already funnels through.
- [x] `common.proto` holds `Paging`, `StringList`, and the shared enums (`BusinessStatus`,
      `PromptStatus`, `RunStatus`, `RunTrigger`, `ResultStatus`, `CompetitorStatus`,
      `CompetitorSource`, `Sentiment`, `MentionSubject`, `MatchMethod`, `CitationSubject`,
      `GenerationStage`) — values read from `internal/store`/its migrations, not from frontend
      string literals. `ProposalStatus` (`generating`/`ready`/`failed`) is the one exception: it is
      workflow-derived in `internal/api/businesses.go`, not `internal/store`'s
      `ProfileProposalStatus` (`pending`/`applied`/`discarded`), which is never exposed over the API.
- [x] `UpdateBusinessRequest`/`UpdateCompetitorAliasesRequest` use `optional` scalars and the
      `StringList` message wrapper so omitted/null/empty PATCH semantics (design 06 Setup notes)
      are representable.
- [x] Every aggregate message carries `repeated string result_ids` (the "every number is a door"
      invariant) under that exact field name; verified exhaustively across all 12 aggregate
      messages plus the 2 singular `result_id`/`latest_result_id` exceptions.
- [x] `PromptResult.request_json` / `.raw_response_json` are `string`, not `google.protobuf.Struct`
      (avoids float-mangling int64 token counts).
- [x] `ProposalPayload`/`ProposedProfile`/`Location`/`ProposedPrompt`/`ProposalSource` mirror
      `internal/llm/propose_profile.go` field-for-field; `internal/llm` itself takes no dependency
      on generated types.
- [x] `buf lint` and `buf generate` pass; generated Go/TS committed. No service implementation yet —
      this story is schema only.

Deps: RPC-1 · Phase 3 · Ref: design 06 (Endpoints by section)

## RPC-3 — Connect plumbing and AuthService

As the developer, I want session auth, CSRF protection, and error mapping working end-to-end for one
real service, so that the remaining services are a mechanical repeat of a proven pattern.

- [x] `/rpc` mounted on the chi router alongside `/healthz` and the untouched `/api/v1` REST tree.
      `isAPIRoute`/`static.go` stay exactly as they are: chi's `Mount("/rpc", …)` registers `/rpc`,
      `/rpc/`, and `/rpc/*` directly, so an unknown `/rpc` path is 404'd by the mounted Connect
      handler itself and never reaches chi's `NotFound` — retargeting `isAPIRoute` to `/rpc` would
      only make unknown `/api/v1` paths wrongly fall through to the SPA. That retarget moves to
      RPC-8, alongside `isAPIRoute`'s deletion, once REST is actually gone.
- [x] Auth interceptor resolves the `opensight_session` cookie into `store.SessionUser` and injects
      it via the existing `withSessionUser`/`sessionUserFromContext` context pair; a public-procedure
      allowlist covers `AuthService.Login` only.
- [x] `connect.WithRequireConnectProtocolHeader()` is the CSRF guard for every `/rpc` call; no RPC is
      marked `idempotency_level = NO_SIDE_EFFECTS` (that would let Connect accept the RPC as a
      header-less GET). `requireRequestedWith` keeps guarding `/api/v1` unchanged — it is not
      "replaced" until RPC-8 deletes the REST tree it guards.
- [x] `internal/api/errors.go`: `rpcError(op string, err error) *connect.Error` covers the two
      sentinels `AuthService` actually reaches — no/expired session → `CodeUnauthenticated`
      (+ a `Set-Cookie` clear via `Meta()`), else `CodeInternal` (real error to `slog`, generic
      message to the client, no `Meta`/details — never an oracle). `store.ErrNotFound`,
      `ErrBusinessNotDraft`, `ErrPromptNotActive`, `ErrPromptLimitExceeded`,
      `WorkflowExecutionAlreadyStarted` aren't reachable from AuthService; RPC-4/5/6 each extend this
      same switch with the sentinel they introduce, rather than this story writing untested branches
      for stores it doesn't call.
- [x] `AuthService` (`Login`, `Logout`, `GetMe`) implemented in a new `internal/api/auth_rpc.go`; the
      uniform-failure/timing-parity behavior in the current `handleLogin` (byte-identical response +
      dummy-hash verify for every failure cause, via a dedicated `rpcLoginFailed()` that never gets
      `Meta`/details attached) survives verbatim.
- [x] `internal/api/middleware.go`'s three context helpers
      (`sessionUserContextKey`/`withSessionUser`/`sessionUserFromContext`) move into
      `internal/api/auth.go`, now shared by both stacks. `requireSession`/`requireRequestedWith`
      stay in `middleware.go`, still wired into `/api/v1` — the file isn't deleted until RPC-8
      removes the REST routes that use them.
- [x] `internal/api/rpc_test.go`: full-stack `httptest.Server` + generated-client tests (cookie-jar
      backed) for the things a direct method call can't cover — session lifecycle (login sets the
      cookie, GetMe/Logout require and consume it, a missing session both 401s and clears the dead
      cookie), missing-`Connect-Protocol-Version` rejection (asserted against the store fake never
      being called, not just the status code), and a descriptor-reflection test that no RPC in the
      whole schema is ever `NO_SIDE_EFFECTS`. `/healthz`, the SPA fallback, and REST's own 404
      handling are already covered by existing tests that now run through a `Routes()` with `/rpc`
      mounted — no need to duplicate them.
- [x] REST `/api/v1` routes untouched and still passing; both stacks run side by side.

Deps: RPC-2 · Phase 3 · Ref: design 07 (auth, CSRF)

## RPC-4 — BusinessService

As a clinic user, I want the business/onboarding endpoints on Connect, so that profile editing and
proposal generation work through the new contract.

- [x] `CreateBusiness`, `GetBusiness`, `UpdateBusiness`, `GetProposal`, `RegenerateProposal`,
      `ApplyProposal` implemented against the existing store seams.
- [x] `internal/api` owns the `llm.ProposalPayload` ⇄ proto conversion both directions; `internal/llm`
      unchanged.
- [x] `UpdateBusiness`'s merge semantics (omitted vs null vs empty, atomic partial column update)
      verified against the actual store behavior, not just the proto shape.
- [x] Existing `businesses_test.go` behavior ported to direct `connect.NewRequest` calls against fakes.
- [x] REST equivalents untouched.

Deps: RPC-3 · Phase 3 · Ref: design 06 (Setup), 03 (onboarding)

## RPC-5 — OverviewService, CitationService, PromptService

As a clinic user, I want the Overview, Citations, and Prompts read/write endpoints on Connect.

- [x] `OverviewService.GetOverview`, `CitationService.ListCitationSources`,
      `PromptService.{ListPrompts,AddPrompt,GetPrompt,ReplacePrompt}` implemented.
- [x] `ReplacePrompt`'s `confirmed: true` requirement (design 06 Prompts) preserved.
- [x] Pagination/filter fields (`limit`, `offset`, `domain`) normalized through a shared helper
      replacing `positiveIntParam`/`nonNegativeIntParam`.
- [x] Existing `overview_test.go`, `citations_test.go`, `prompts_test.go` behavior ported.

Deps: RPC-3 · Phase 3 · Ref: design 06 (Overview, Prompts)

## RPC-6 — CompetitorService, ResultService

As a clinic user, I want the Competitors and Responses/Runs endpoints on Connect — the full backend
surface is now on Connect, REST still live behind it.

- [x] `CompetitorService.{ListCompetitors,AddCompetitor,SetCompetitorStatus,
      ReviewSuggestedAlias,UpdateCompetitorAliases}` implemented. `SetCompetitorStatus` (status
      arg) and `ReviewSuggestedAlias` (decision arg) merge the track/dismiss and approve/reject
      pairs into one RPC each — the server already funnels each pair through one function today,
      and the frontend already calls both through one function with a discriminator argument, so
      this keeps the RPC count matching the existing call shape (schema decision from RPC-2).
- [x] `ResultService.{ListRuns,ListResults,GetResult}` implemented, including the `mentioned` filter
      and the succeeded-but-unanalyzed badge flag.
- [x] Existing `competitors_test.go`, `responses_test.go` behavior ported.
- [x] All 7 services now live under `/rpc`; REST `/api/v1` still mounted and unchanged (deleted in
      RPC-8, after the frontend cutover).

Deps: RPC-3 · Phase 3 · Ref: design 06 (Competitors, Responses, Runs)

## RPC-7 — Frontend cutover

As a clinic user, I want the SPA talking to `/rpc` instead of `/api/v1`, so that the frontend gets
generated types and connect-query's derived cache keys.

- [x] `web/src/api/transport.ts`: `createConnectTransport({ baseUrl: "/rpc" })`;
      `<TransportProvider>` wraps `<QueryClientProvider>` in `main.tsx`.
- [x] All 13 consumer files (`app-layout`, `app-sidebar`, `response-drawer`,
      `citation-sources-drilldown`, and the competitors/login/onboarding/overview/prompts/responses/
      setup pages) ported to connect-query hooks.
- [x] `enabled: businessId !== undefined`, `placeholderData: keepPreviousData`, data-driven
      `refetchInterval`, and manual invalidation (`useInvalidateCompetitorViews`) idioms preserved,
      the last via `createConnectQueryKey` instead of string literals.
- [x] `useAllCompetitors`'s client-side pagination loop kept as a hand-written hook over a plain
      `createClient` (connect-query has no equivalent).
- [x] Date rendering switched to `timestampDate()`; status comparisons switched to generated enums.
      Every place a status/enum value is *rendered* as text (not just compared) gets an explicit
      label map — `tsc` cannot catch a numeric enum leaking into a React child or template literal,
      so this needs a deliberate audit of the drawer, prompts, and responses pages, not just a
      mechanical find-and-replace.
- [x] `ApiError` call sites replaced with `ConnectError.from(err)`, reading `.rawMessage` (not
      `.message`, which is code-prefixed, e.g. `"[unauthenticated] invalid email or password"` — the
      prefixed form must never reach the UI) and `.code === Code.Unauthenticated`.
- [x] `web/vite.config.ts` proxies `/rpc` (kept alongside the existing `/api` proxy — REST is still
      live until RPC-8, which also removes this entry).
- [x] `web/src/api/{client,auth,businesses,overview,prompts,competitors,responses,citations,
      onboarding}.ts` deleted (resolves the `PromptSummary` name collision as a side effect).
- [x] `npm run typecheck && npm run build && npm run lint` pass; full manual walkthrough (login →
      onboarding → all 5 sections) confirms no behavior regression, including a check that zero
      requests hit `/api/v1/**` for the whole session — the cheapest, highest-signal proof the
      cutover is actually complete.

Deps: RPC-4, RPC-5, RPC-6 · Phase 3 · Ref: design 06 (Frontend stack)

## RPC-8 — Delete REST

As the developer, I want the old REST stack removed, so that there is exactly one contract.

- [ ] `internal/api`'s REST handler bodies, request/response DTOs, and the `/api/v1` route block
      deleted; `Routes()` left with `/healthz`, `/rpc`, and the SPA fallback only.
- [ ] `writeProblem`/`writeJSON`/`writeStoreError`/`writeInternalError` removed in favor of `rpcError`
      (`writeProblem` may survive narrowly if `NotFound`/`MethodNotAllowed` still need it — check).
- [ ] `internal/api/middleware.go` deleted (`requireSession`/`requireRequestedWith`/`isStateChanging`
      have no REST caller left; their Connect-side equivalents, added in RPC-3, are unaffected).
- [ ] `isAPIRoute` deleted along with the `/api/v1` block it exists to distinguish from the SPA
      fallback — the `/rpc` mount 404s its own unknown paths and needs no equivalent check.
- [ ] `web/vite.config.ts`'s `/api` dev-proxy entry removed (RPC-7 kept it alongside `/rpc` while
      REST was still live; nothing calls it after RPC-7's cutover).
- [ ] `go test ./...` and `npm run build` pass with REST fully gone.

Deps: RPC-7 · Phase 3 · Ref: design 06

## RPC-9 — Docs

As the developer, I want the design docs to reflect Connect RPC as the current, only contract, so
that they remain the finalized plan rather than a stale record of REST.

- [ ] `docs/design/01-architecture.md`: replace the "no gRPC/Connect for MVP" line with the Connect
      decision and rationale.
- [ ] `docs/design/06-api-frontend.md`: replace the REST conventions section and endpoint table with
      the service/RPC list and proto schema rules; update the `web/src/` tree to show `gen/`.
- [ ] `docs/design/07-cross-cutting.md`: replace the `X-Requested-With` CSRF description with
      `WithRequireConnectProtocolHeader` and the no-`NO_SIDE_EFFECTS` constraint; note the codegen
      step in the CI/deploy description.
- [ ] `docs/dev.md`: document `make proto`.
- [ ] `docs/stories/05-responses-ui.md` (WEB-1/WEB-2): update the stale "typed API client"/`/api/v1`
      references to point at this epic instead.

Deps: RPC-8 · Phase 3 · Ref: AGENTS.md (docs hold only the finalized plan)
