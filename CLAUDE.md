# Project rules

These are enforceable rules for working in this repo, not aspirational guidance. Where a rule references a doc, that doc is the source of truth and this file is the summary — if they disagree, fix the drift rather than picking one silently.

## Project management

Work is tracked in GitHub, not by editing checkboxes into design docs:

- [V1-ROADMAP.md](docs/V1-ROADMAP.md) is the design source of truth for *what* the phases contain and why they're sequenced that way — it does not track completion state.
- Progress is tracked via [GitHub milestones](https://github.com/rhysmcneill/agentic-idp/milestones) (one per phase) and their issues, each carrying a checklist mirrored from the roadmap.
- The [project board](https://github.com/users/rhysmcneill/projects/1) gives a Todo/In Progress/Done view across all of them.
- If a roadmap phase changes, update the doc first, then reconcile the matching issue's checklist in the same PR — don't let them drift apart (see the rule above).

## Comments

Default to **no comment**. Well-named identifiers document what code does; a comment restating that is noise that rots.

Write a comment only when the **why** is not recoverable from the code:

- A security invariant that must not be violated
- A non-obvious constraint imposed by an external system
- A subtle invariant a reader would otherwise break
- A workaround, with what it works around
- Behaviour that would genuinely surprise a competent reader

Never:

- Multi-paragraph comment blocks or multi-line docstrings
- Restating the signature or the next line in prose
- References to tasks, tickets, PRs, or "added for X" — that belongs in commit messages
- Section banners and decorative separators

Exported identifiers in `pkg/` get **one line**, since that package is public API and an extraction candidate. Skip it when the name already says it. Everything in `internal/` gets a comment only under the rules above.

If deleting a comment would not confuse a future reader, delete it.

## Security invariants

These are load-bearing. Changing code that touches them requires re-reading [docs/SECURITY-MODEL.md](docs/SECURITY-MODEL.md) first.

- `sts:AssumeRole` is called **only** from `worker/internal/broker/aws`. Nowhere else, ever. It implements `pkg/cloud.Broker` — a provider-agnostic interface with only the AWS side built in v1 (decision 017) — so never let AWS-specific assumptions leak into `pkg/cloud` itself.
- The control plane never holds or receives cloud credentials.
- Delegation and authority come from **verified token claims**, never from a request body or parameter. Anything crossing a boundary the far side could forge is audit-only.
- The MCP server forwards the agent's token. It never substitutes its own identity.
- An actor cannot grant an agent more authority than it holds itself.
- Agents cannot enrol agents by default (see [docs/AGENT-MODEL.md](docs/AGENT-MODEL.md)) — recursive delegation launders authority.
- Credential-scoping failures **fail closed**. Never fall back to a broader role.
- Every credential mint is an audited event: actor, action, granted scope, TTL.
- Secrets (tokens, role credentials, webhook signing keys) are never logged, never written to disk by the control plane, and never appear in error messages returned to a client. Use `pkg/ci.Secret`'s pattern (redacted `String`/`MarshalJSON`, explicit `Reveal`) for any new credential type.
- Validate and authorise at the boundary (API handler, MCP tool, CLI command). Code below that boundary trusts its caller *within the process* — do not re-check authorisation at every layer, but never skip it at the boundary.
- Any new external input (webhook payload, OIDC token, catalog YAML) is untrusted until parsed into a typed value and validated. Do not pass raw maps/JSON deeper than the boundary that received them.

## Structure

- One top-level directory per deployable. See [docs/REPO-STRUCTURE.md](docs/REPO-STRUCTURE.md), which is canonical.
- Components talk over the control plane API. Never import another component's `internal/`.
- `pkg/` is public API — it should make sense to a stranger importing it alone.
- New packages are `internal/` by default. Promote to `pkg/` only when a second, genuinely external consumer exists or is imminent (see the BSL/Apache extraction plan in [docs/DECISIONS.md](docs/DECISIONS.md) 012).
- Prefer small interfaces defined at the point of use (Go convention) over large interfaces defined at the point of implementation. `pkg/ci.Adapter` is the template: shaped by real provider differences, not guessed upfront — see decision 008.
- No circular dependencies between internal packages. If two packages need each other, the shared part belongs in a third package or one of them owns the relationship.

## Go standards

- `gofmt` and `go vet` clean before any commit. `golangci-lint` once configured (Phase 0) — do not merge code that would fail it.
- Errors: wrap with `%w` and context (`fmt.Errorf("registering environment: %w", err)`), never swallow silently, never `panic` outside of `main` wiring/startup validation and truly unrecoverable invariant violations (e.g. `NewRegistry`'s duplicate-provider panic — a wiring bug, not a runtime condition).
- Sentinel errors (`Err...`) live next to the package that returns them, are checked with `errors.Is`/`errors.As`, and are documented with one line explaining when they occur — see `pkg/ci/errors.go`.
- Every exported function that does I/O or can be cancelled takes `context.Context` as its first parameter.
- No global mutable state. Dependencies (DB handle, HTTP client, clock) are passed in, not reached for — this is what makes the security invariants above testable rather than asserted.
- Prefer table-driven tests. A new package ships with tests in the same PR, not after.
- Struct fields and function signatures favour explicit types over `map[string]any` past the boundary that parsed the input (see the untrusted-input rule above).

## Testing

- Every package under `pkg/` and every security-invariant-touching path in `internal/` needs unit tests before merge.
- The Phase-by-phase verification criteria in [docs/V1-ROADMAP.md](docs/V1-ROADMAP.md) are acceptance tests, not documentation to read and forget. When a phase claims a milestone, there is a test (or a documented manual run) proving it — e.g. Phase 1's "forge `delegated_by` and confirm it's ignored" and "deny a tier and confirm the pipeline fails closed."
- Prefer a fake/in-memory implementation of external dependencies (AWS STS, a CI provider) over mocks-that-assert-call-order. Table-driven tests against `pkg/ci.Adapter` should run identically against every real adapter — that is what makes the interface trustworthy.
- Integration tests that need real AWS or a real CI provider are tagged and skipped by default; they run against disposable/sandbox accounts only, never against anything resembling production.

## API design

- REST, OpenAPI-documented, per [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). Every mutating endpoint is idempotent where the caller can supply an idempotency key — required for anything a retrying agent might call twice.
- Long-running operations return a run ID immediately and are polled; nothing blocks on execution (the async run model in ARCHITECTURE.md). This applies to the CLI and MCP surfaces too, not just REST.
- Breaking changes to the API require a version bump, not a silent change — self-hosted customers control their own upgrade timing and a silently broken integration is a support incident, not a deploy.
- Errors returned to clients are structured (a stable code plus a human message), never a raw Go error string — raw errors leak internal detail and are not stable enough for a CLI/agent to branch on.

## Database & migrations

- PostgreSQL. Every migration is forward-only in normal operation; a broken migration is fixed with a new migration, not edited in place, once it has shipped to any environment beyond a local dev box.
- `tenant_id` is present on every tenant-scoped table from the first migration (decision 001) — do not add a table without it on the assumption "we're single-tenant for now."
- No destructive migration (`DROP COLUMN`, `DROP TABLE`, a `NOT NULL` without a backfill) ships without a stated rollback path, because customers self-host and control their own upgrade timing — we cannot assume we can just fix it in the next release before anyone notices.
- The audit log table is append-only at the schema level (no `UPDATE`/`DELETE` grants for the application role), not only by application convention — see the audit invariant above.

## Observability

- Structured logging (key-value, not string interpolation) from the first service, so log fields are queryable rather than grep-guessed later.
- Every audited action and every credential mint (see Security invariants) is a structured audit record, not a log line — audit and operational logs are different data with different retention and access-control needs; do not conflate them.
- Telemetry from self-hosted installs is opt-in and clearly disclosed, per decision — see [docs/PLAN.md](docs/PLAN.md) "no feedback loop unless telemetry is built in." Never phone home silently.

## Dependencies & supply chain

- New dependencies are a deliberate choice, not a default: prefer the standard library, then a well-maintained dependency with a real community, over a small utility package pulled in for one function.
- Pin versions (`go.sum` committed, no floating versions). For the frontend (Phase 3), lockfile committed.
- Before adding a dependency that will touch credentials, cloud APIs, or parse untrusted input (OIDC tokens, webhook payloads, YAML catalog entries), check its maintenance status and license compatibility with BSL/Apache — do not introduce a GPL or similarly viral dependency into code we intend to relicense or commercialise.
- Dependabot/Renovate-equivalent should be configured once `.github/workflows/` has real CI (see below) — flag it as a Phase 0/1 task rather than deferring indefinitely.

## Git & commits

- Small, reviewable commits. A commit message states *why*, not a restatement of the diff — this is also where task/PR references belong (see Comments above: they do not belong in code).
- No secrets, real AWS account IDs, or customer-identifying information in commit history, ever — treat history as permanent and world-readable, because under BSL the source is.
- Every commit that touches a file under Security invariants above gets a commit message that says which invariant it relates to.

## CI/CD (our own, not the customer's)

Full placement of CI, Dockerfiles, image publishing, versioning and Dependabot/Codecov across phases lives in [docs/DELIVERY.md](docs/DELIVERY.md) — not repeated here. The rules that apply regardless of phase:

- A repo-root `Makefile` is the single interface for lint, format, build, test, docker and publish commands — CI workflows call `make <target>`, not inlined shell, so a contributor gets the exact same command locally that CI runs. Based on [ssmctl](https://github.com/rhysmcneill/ssmctl/blob/main/Makefile)'s Makefile (fmt/vet/lint/test/setup/ci targets reused as-is), extended with per-service `build-<service>`/`docker-build-<service>`/`publish` targets for this repo's multiple deployables. Frontend targets join it once the frontend exists (Phase 3).
- `pre-commit` runs the fast Go gates (`go vet`, `golangci-lint`, `gosec`), general hygiene checks, `detect-secrets`, and `commitlint` before a commit lands — same shape as [ssmctl](https://github.com/rhysmcneill/ssmctl)'s config, mirroring the Makefile targets rather than re-encoding the checks separately. Lands in Phase 0.
- Security-sensitive packages (`pkg/ci`, `pkg/identity`, `pkg/ciauth`, `pkg/cloud`, `worker/internal/broker`) get a required review once there is more than one contributor — self-review is fine solo, but the rule should exist in workflow config so it activates automatically the moment it isn't just you.
- Never commit `--no-verify` or skip CI to unblock a merge; fix the underlying failure.
- Publishing credentials (registry push, release tokens) live only in CI secrets, are scoped to `main`/tag-triggered jobs, and are never in scope for a PR build.

## Frontend (from Phase 3)

- TypeScript strict mode, no `any` at a component boundary that receives API data — parse into a typed shape the same way the backend rule above requires.
- Every colour, spacing and type token comes from the design system `impeccable extract` produces (see [docs/V1-ROADMAP.md](docs/V1-ROADMAP.md) Phase 3), not ad hoc values in component files — this is what keeps the semantic-colour safety property (policy outcomes, trust tiers, run states) consistent across the whole UI.
- No business logic in the frontend. It is a thin client over the REST API, per ARCHITECTURE.md.

## Docs

`docs/` holds the design and is kept current. When a decision changes, update [docs/DECISIONS.md](docs/DECISIONS.md) rather than silently diverging. If code and a doc disagree, that is a bug in one of them — fix the drift in the same PR that caused it, don't leave it for later.
