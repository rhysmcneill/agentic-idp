# Architecture

## Topology

```
┌────────────────────────────────┐      ┌──────────────────────────────────┐
│    Customer infrastructure     │      │       Customer AWS account       │
│                                │      │                                  │
│  ┌──────────────────────────┐  │      │  ┌────────────────────────────┐  │
│  │      Control plane       │  │      │  │          Worker            │  │
│  │      (Go, Postgres)      │◄─┼──────┼──┤  broker: sts:AssumeRole    │  │
│  │                          │  │ poll │  │  ciauth: OIDC callbacks    │  │
│  │  identity, policy, runs, │  │ (out)│  │  creds in memory only      │  │
│  │  approvals, audit, cost  │  │      │  └────┬──────────────▲────────┘  │
│  │                          │◄─┼──┐   │       │ trigger      │ OIDC      │
│  │  NO cloud credentials    │  │  │   │       │              │ callback  │
│  └────────────▲─────────────┘  │  │   │  ┌────▼──────────────┴────────┐  │
│               │                │  │   │  │   Their CI (GH Actions)    │  │
│  ┌────────────┴─────────────┐  │  │   │  └────────────────────────────┘  │
│  │   MCP server (separate   │  │  │   │                                  │
│  │   pod — forwards the     │  │  │   └──────────────────────────────────┘
│  │   agent's token, holds   │  │  │
│  │   no privilege of its    │  │  └─── "is correlation X approved,
│  │   own)                   │  │        and at what tier?"
│  └────────────▲─────────────┘  │
└───────────────┼────────────────┘
                │  MCP
        ┌───────┴────────┐
        │  Their agent   │   (Claude Code, Cursor, Devin…)
        │  their model,  │
        │  their keys    │
        └────────────────┘
```

The control plane **never holds customer cloud credentials**. The worker does, in memory, briefly. The MCP server holds no privilege at all.

## Components

See [REPO-STRUCTURE.md](REPO-STRUCTURE.md) for how these map onto the tree.

**Control plane** (`controlplane/`) — the single source of truth. Identity, policy, run state, approvals, audit, cost. Serves the REST API consumed by the CLI, the MCP server and (later) the frontend. **Holds no cloud credentials.**

**MCP server** (`mcp/`) — a **separate service, pod and container**, not embedded in the control plane. Exposes governed actions as agent tools, with the tool list filtered by the calling agent's tier. Async request/poll: an agent cannot hold a forty-minute tool call, so tools return a run ID.

It is a thin, unprivileged translator. It **forwards the agent's token** rather than authenticating on its own behalf — if it substituted its own identity, the delegation chain would terminate at the MCP server and the audit story would fail.

**Worker** (`worker/`) — customer-deployed single binary, and the only component that holds cloud credentials. Polls the control plane outbound for approved jobs. Assumes the tier-appropriate IAM role via `internal/broker`, holds temporary credentials in memory only, triggers and tracks CI runs, reports back.

It also **receives OIDC callbacks from the customer's pipelines** (`internal/ciauth`), since brokering credentials requires calling STS and only the worker may do that. The worker sits in the customer's account alongside their CI, so this is an internal boundary; the link that crosses a trust boundary — worker to control plane — remains outbound-only.

**CLI** (`cli/`) — `idpctl`. Used by humans and scriptable by agents that do not speak MCP.

**Frontend** (`frontend/`) — React and TypeScript, Phase 3. A thin client over the API with no business logic.

Every component reaches the same governed service layer through the control plane API. There is one governed code path, not one per client, and no component imports another's `internal/`.

## Data model

Core entities and the relations that matter. See [DATA-MODEL.md](DATA-MODEL.md) for the table-level design (columns, constraints, per-provider split).

- `Tenant` — present from the first migration even though v1 ships self-hosted single-tenant. Retrofitting it is expensive; carrying it is nearly free.
- `Team` — catalog entries need owners and approvals need somewhere to route. Actors and tenants alone are insufficient.
- `Actor` — `{ type: human | agent, id, team, trust_tier }`. Agents additionally carry a delegation claim, read **from the verified token, never from the request body**.
- `Environment` — an AWS account/region. Stores role ARNs per tier, external ID, trust-anchor principal. **Never static keys.**
- `CatalogEntity` — a service. What exists, who owns it.
- `Binding` — the three-way relation `entity ↔ environment ↔ pipeline`. This is how the catalog becomes an action surface.
- `Run` — a governed execution. See the state machine below.
- `AuditEvent` — append-only. Every action *and every credential mint*.

## Why the catalog connects to CI

A catalog alone needs no CI. It is a directory, populated from Git.

CI becomes necessary when the catalog stops being a directory and becomes an **action surface**:

- **Catalog** answers *what exists and who owns it.*
- **CI** answers *what can be done to it, right now, by this actor.*
- **Trust tier is the join**: `actor + entity + action → allow | deny | needs-approval`

Neither half is interesting alone, which is also why the four-vendor stack exists today.

## Run model

Everything user-facing is asynchronous.

```
requested → policy_checked → awaiting_approval → queued → executing
                    │                                        │
                    └── denied                               ├── succeeded
                                                             ├── failed
                                                             ├── cancelled
                                                             └── timed_out
```

Durable run records, incremental log capture, cancellation, timeouts, idempotency keys. The REST API, CLI and MCP tools all return a run ID and poll.

Approvals attach to a run. **The approver must see the change diff** — which promotes log capture from a nice-to-have to load-bearing infrastructure.

## CI integration

This *is* the v1 executor, not a side integration. Four distinct capabilities:

### Trigger
Per-provider adapters, because the mechanisms differ fundamentally:

| System | Trigger | Returns |
|---|---|---|
| GitHub Actions | `workflow_dispatch` | `204 No Content` — **no run ID**; must be discovered by correlation |
| Jenkins | `buildWithParameters` | A queue item to resolve into a build number |
| GitLab CI | Trigger token / API | Pipeline object with ID immediately |
| Buildkite, Spacelift, env0, Bitbucket Pipelines | REST | Run/build with ID immediately |
| Atlantis | **PR comment** — no API at all | Nothing; correlate via PR |

The worker triggers, not the control plane, because it can reach self-hosted CI. It therefore holds CI credentials alongside cloud credentials, consistently.

### Observe
**Polling by default.** Webhooks require an inbound endpoint, which conflicts with the outbound-only worker and is frequently unreachable in self-hosted installs. Treat webhooks as an optimisation where the control plane happens to be addressable.

### Gate
Gating *before* trigger is trivial but bypassable by anyone pushing directly. Gating *inside* the pipeline via a required status check is unbypassable but requires a workflow change. Lean on GitHub's native required status checks and environment protection rules rather than reinventing them.

### Identity propagation
See [SECURITY-MODEL.md](SECURITY-MODEL.md) — this is the load-bearing one.

## CI adapter interface

Generic from day one (`pkg/ci`), with **GitHub Actions as the only implementation**. Others on demand, never speculatively.

The design constraint that shapes the interface: triggering is *not* uniformly "call API, get run ID". Three shapes exist (returns ID, returns nothing, returns an intermediate handle), so trigger and resolve must be separate operations.

```go
type Adapter interface {
	Provider() Provider
	Capabilities() Capabilities

	ValidateConfig(ctx context.Context, cfg Config) error

	// Trigger MUST inject req.Correlation into the external run.
	Trigger(ctx context.Context, cfg Config, req TriggerRequest) (Handle, error)

	// Resolve discovers the external run by Correlation for providers whose
	// Trigger returns no ID. Returns h unchanged when TriggerReturnsID is true.
	Resolve(ctx context.Context, cfg Config, h Handle) (Handle, error)

	Status(ctx context.Context, cfg Config, h Handle) (RunStatus, error)
	Logs(ctx context.Context, cfg Config, h Handle, offset string) (LogChunk, error)
	Cancel(ctx context.Context, cfg Config, h Handle) error
}
```

Non-obvious decisions baked into this:

- **`Config` passed per call, not held on the adapter.** Adapters are stateless singletons; config is per-Environment. A worker may serve several environments.
- **`Correlation` is mandatory for every adapter.** It is our run ID, injected into the external run. Dual purpose: it discovers runs for providers that return no ID, *and* it matches the pipeline's OIDC callback to the run record. Making it universal keeps the credential-brokering path identical across providers.
- **`ActorRef` is audit-only, never authorisation.** Same discipline as delegation claims: anything crossing a boundary where the other side could forge it must not feed authorisation.
- **Normalised `Status` plus provider-native `Raw`.** GitHub splits `status` and `conclusion`; Jenkins has `UNSTABLE` which maps to nothing clean. Keep the native value for audit rather than losing information.
- **Log offset is an opaque `string`, not an int.** GitHub uses per-job cursors and serves completed logs as a zip; Jenkins uses byte offsets; GitLab uses ranges. An int would leak one provider's model into the interface.
- **`Capabilities` rather than `ErrUnsupported` everywhere.** Atlantis has no trigger API and no cancel. The run engine needs to know before it schedules a poll loop or renders a UI affordance.

## Cloud credential abstraction

AWS is the only implemented provider in v1, but the credential-minting code is written behind a provider-agnostic interface from the start — the same pattern as `pkg/ci.Adapter` (decision 008), applied for the same reason.

```go
// pkg/cloud
type Provider string // "aws" | "gcp" | "azure"

type Broker interface {
	Provider() Provider
	ValidateEnvironment(ctx context.Context, cfg EnvironmentConfig) error
	MintCredentials(ctx context.Context, cfg EnvironmentConfig, tier identity.Tier) (Credentials, error)
}
```

`worker/internal/broker/aws` implements it. This is not full multi-cloud support — GCP and Azure remain deferred (see [V1-ROADMAP.md](V1-ROADMAP.md)) — but it means adding them later is additive rather than a rewrite of the AWS-specific code path.

**Why this isn't a bigger v1 commitment.** AWS, GCP and Azure don't share an "assume role" primitive that a thin abstraction can paper over: `sts:AssumeRole` + external ID, GCP Workload Identity Federation + service account impersonation, and Azure Entra ID federated credentials + MSAL are three genuinely different credential mechanisms, with three different scoping primitives (AWS session policies vs. GCP IAM conditions vs. Azure subject-claim matching) and three different Kubernetes identity-mapping problems (EKS access entries vs. GKE Workload Identity bindings vs. AKS federated credentials — each is the reason decision 006 needed role-per-tier, restated in that provider's own terms). Implementing all three properly, including a real sandbox account per provider to test credential minting against, is roughly 2–3x the Phase 0/1 broker and onboarding work — not a linear extension. See decision 017.

### Connectivity check

The Phase 0 milestone in one flow: an actor triggers a check, the worker actually calls `sts:AssumeRole` for each tier, and the result comes back via the same request-a-job/poll-for-a-result *shape* as the "Run model" above — trigger, async completion, poll — but deliberately **not** the same mechanism: the Phase 1 run state machine and Postgres-backed job queue don't exist yet ([V1-ROADMAP.md](V1-ROADMAP.md) puts them in Phase 1), so this uses its own small, single-purpose `environment_verifications` table rather than pulling that forward early. `POST /v1/environments/{name}/verify` and `GET /v1/environments/{name}/verifications/{id}` are plain authenticated REST endpoints; `idpctl environment verify` is their only client today, but a Phase 3 UI surface ("Environment management — connectivity status", [V1-ROADMAP.md](V1-ROADMAP.md)) becomes a second client of the same two endpoints, the way the Phase 3 setup UI reuses `POST /v1/setup` rather than getting its own mechanism.

```
Operator client (idpctl today, UI Phase 3)
        │
        │ 1  POST /v1/environments/{name}/verify
        │    Authorization: Bearer <human/agent token>
        ▼
┌───────────────────────────────────────────┐
│               Control plane                │
│  environment_verifications row created,     │
│  status = pending                           │
└───────────────────▲─────────────────────────┘
                     │
                     │ 2  GET /v1/worker/verifications/next
                     │    Authorization: Bearer <worker credential>
                     │    checked by requireWorkerAuth, not
                     │    identity.Verifier — see decision 019
                     │    and SECURITY-MODEL.md
                     │
┌────────────────────┴─────────────────────────┐
│  claims oldest pending row (FOR UPDATE SKIP    │
│  LOCKED), returns the job plus the             │
│  environment's AWS config (account_ref,        │
│  external_id, trust_anchor, role ARNs)         │
└────────────────────┬───────────────────────────┘
                      ▼
┌────────────────────────────────────────────────────────────┐
│              Customer AWS account — Worker                  │
│  3  for each tier: broker/aws.MintCredentials(cfg, tier)     │
│         └─ sts:AssumeRole ─► customer IAM role               │
│     record {ok, error} per tier — no fallback to a broader   │
│     role if one tier's AssumeRole fails                      │
└──────────────────────────┬────────────────────────────────────┘
                            │
                            │ 4  POST /v1/worker/verifications/{id}/result
                            │    Authorization: Bearer <worker credential>
                            ▼
                 ┌─────────────────────────────┐
                 │        Control plane         │
                 │  status → succeeded/failed   │
                 │  audit_events: environment.  │
                 │  verify.completed            │
                 └───────────────▲───────────────┘
                                 │
                                 │ 5  GET /v1/environments/{name}/verifications/{id}
                                 │    Authorization: Bearer <human/agent token>
                                 │
                     Operator client polls/renders the result
```

## Execution strategy

**v1 delegates to the customer's existing CI.** No Terraform state, locking, concurrency or drift handling on our side — the single largest build-cost saving available, and state management is precisely why Spacelift, env0, Scalr and HCP exist as companies.

**PR-based by default.** An agent proposes a change → PR → policy check on the diff → human approves the diff → merge triggers the pipeline. This yields audit, the plan/apply approval gap, review UX and rollback almost for free, and it is the model platform teams already trust.

Native `TerraformExecutor` and `K8sExecutor` are additive behind the same interface, later, only if the integration ceiling actually binds.

## Technology

| Layer | Choice | Note |
|---|---|---|
| Control plane, worker, CLI | Go 1.27 | Single module at root; split only if genuinely needed |
| API | REST, OpenAPI-documented | Plus the async run model |
| Job queue | Postgres-backed (`FOR UPDATE SKIP LOCKED`, or River) | Avoid adding Kafka/NATS |
| Policy | **Typed trust tiers in Go for v1**; OPA/Rego from Phase 2 | Rego is a real learning curve; typed tiers ship faster and lose no optionality |
| Database | PostgreSQL | |
| Frontend | React + TypeScript (Vite) | Phase 3 |
| Deployment | Helm, Docker Compose, single worker binary | |
| Human auth | Operator-set local admin (`idpctl setup`) → OIDC (Phase 2) | See [Decision 018](DECISIONS.md) |
| Agent auth | Short-lived tokens with bound delegation claims | |
