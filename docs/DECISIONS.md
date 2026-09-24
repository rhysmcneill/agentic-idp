# Decision Log

Numbered, append-only. Each records what was decided and *why*, so the reasoning survives when the decision is later questioned. Supersede rather than edit.

---

### 001 — Self-hosted first, one codebase, `tenant_id` from commit one

**Decision.** Customers host the platform. A hosted offering may follow later as the same binary, not a second product. `tenant_id` exists in the schema from the first migration regardless.

**Why.** The product brokers cloud credentials; "runs in your account, we never see your credentials" is a far easier first sale than asking a company to trust a young vendor with cross-account production access. It also defers SOC 2 ($20–50k, several months) entirely. Carrying `tenant_id` from the start costs nearly nothing; retrofitting it costs a migration across every table.

**Consequence.** No SLA. Version sprawl and no feedback loop unless telemetry is built in deliberately. Enterprise sales motion rather than self-serve.

---

### 002 — Delegate execution to the customer's CI; do not run Terraform natively

**Decision.** The v1 executor triggers pipelines the customer already runs. Native Terraform and Kubernetes executors are additive behind the same interface, later, only if the integration ceiling binds.

**Why.** Running Terraform means owning state backends, locking, concurrency, workspaces and drift — realistically the majority of the engineering, and precisely why Spacelift, env0, Scalr and HCP exist as companies. Delegating sidesteps all of it.

**Consequence.** CI integration becomes the execution layer rather than a side feature. Introduces the CI credential-inheritance hole, addressed in 004.

---

### 003 — Control plane plus a customer-run worker

**Decision.** A worker binary runs in the customer's own account, polls the control plane outbound, assumes IAM roles and executes. The control plane never holds customer cloud credentials.

**Why.** The industry-standard pattern — Spacelift, env0, Terraform Cloud agents, GitHub Actions self-hosted runners, Buildkite, Atlantis all converge on it, because customers will not let a vendor control plane hold their cloud credentials. It also places code execution inside the customer's existing trust boundary, so sandboxing is not our problem to solve.

**Consequence.** Two deployables. Polling rather than webhooks as the default observation mechanism.

---

### 004 — Pipelines fetch tier-scoped credentials via a CI OIDC callback

**Decision.** Triggered pipelines must not use their own static OIDC role. They call back with their CI OIDC token; the control plane verifies it, matches the correlation ID to the approved run, and brokers credentials scoped to the *triggering actor's* tier. It fails closed.

**Why.** Without this, an agent triggering a pipeline inherits that pipeline's typically-broad permissions regardless of its trust tier — the governance model leaks entirely at the CI boundary and every other guarantee becomes decorative.

**Consequence.** Customers must modify workflows to fetch credentials from us. This is the single biggest adoption risk in the v1 strategy and the first thing to validate with a design partner.

---

### 005 — Delegation is a bound token claim, never a request field

**Decision.** An agent's "acting on behalf of" claim is cryptographically bound inside its token at mint time. Any `delegated_by` value in a request body is ignored.

**Why.** A self-asserted delegation chain is worthless for audit. If the agent can claim its own authority, the entire attribution story fails. Caught during pre-implementation review, when it was still free to fix.

**Consequence.** Token minting needs an explicit authorising principal. Generalises to a rule: anything crossing a boundary where the other side could forge it must never feed authorisation.

---

### 006 — One IAM role per trust tier, not session-policy scoping

**Decision.** Each trust tier gets a distinct IAM role in each environment.

**Why.** Session policies are capped around 2048 characters packed, can only restrict never grant, and role chaining caps sessions at one hour. Decisively: **EKS access entries map the role**, so a single role across tiers lands every tier as the same Kubernetes identity and K8s RBAC cannot tell an agent from a human.

**Consequence.** The onboarding Terraform module creates several roles per environment rather than one.

---

### 007 — Typed trust tiers in Go for v1; OPA from Phase 2

**Decision.** Policy starts as typed Go code. OPA/Rego arrives in Phase 2 as the custom-policy escape hatch.

**Why.** Rego is a genuine learning curve and the tier model is simple enough not to need it yet. Typed tiers ship faster and forfeit no optionality — OPA can be added behind the same policy interface.

---

### 008 — Generic CI adapter interface, one implementation

**Decision.** `pkg/ci` defines a provider-agnostic adapter interface from day one; GitHub Actions is the only implementation. Others are added on demand, never speculatively.

**Why.** CI systems differ in ways that break naive interfaces — GitHub Actions `workflow_dispatch` returns no run ID, Jenkins returns a queue item, Atlantis has no API at all. Discovering that when adding the *second* adapter would force a rewrite. But building six adapters up front is commodity plumbing and a maintenance treadmill.

**Consequence.** Trigger and resolve are separate operations; correlation IDs are mandatory for every adapter.

---

### 009 — General drift detection is out of scope; unattributed-change detection is in

**Decision.** We do not answer "does actual state differ from desired state." We do answer "did something change that did not come through a governed run."

**Why.** Drift needs state access and scheduled plans, is unrelated to agent governance, and is thoroughly served by Firefly, Spacelift, env0 and HCP. Attribution is answerable from CloudTrail plus our own run records with no state access at all — and it catches the failure mode people actually fear about agents.

---

### 010 — Production is in scope

**Decision.** The platform governs production changes. Agents operate unattended only in non-production; in production they may propose, and a human approves.

**Why.** Trust tiers are meaningless with nothing risky to tier against, approvals are pointless if nothing needs approving, and nobody is afraid of an agent breaking staging. A staging-only version is not a smaller product, it is a product with no buyer.

**Consequence.** Staging-only remains a legitimate go-to-market wedge, but the architecture assumes production from day one.

---

### 011 — Break-glass is a design requirement, despite no SLA

**Decision.** No uptime obligation — customers operate the platform. But tier roles must remain independently assumable by a documented emergency principal the customer's security team controls.

**Why.** If the broker is the only path to deploy credentials, then when it is down the customer cannot deploy the fix that restores it. "You are responsible for uptime" does not answer that, and security review will ask regardless of who hosts it.

---

### 012 — BSL 1.1, single repo; extract an Apache library later

**Decision.** BSL 1.1 at the root, Change Date four years out converting to Apache 2.0, with an Additional Use Grant making non-production use free. A CLA is required before the first external PR. An Apache 2.0 identity/audit library is extracted around Phase 1–2.

**Why.** Pure proprietary is unviable — platform teams will not run a closed binary brokering production credentials. Apache everywhere has no revenue mechanism for self-hosted software. Open-core requires drawing and defending a feature boundary with no customers to guide it. BSL is one codebase and one licence, materially less work.

The ordering is driven by an asymmetry: **restrictive → permissive is easy; permissive → restrictive is effectively impossible**, because anyone can fork the last permissive commit and contributors' code cannot be relicensed without consent.

**Consequence.** The library boundary is discovered from working code rather than guessed at now.

---

### 013 — Pricing meters agent identities

**Decision.** Human seats are cheap or free. Agent identities are the billable unit.

**Why.** On-thesis: it grows with the customer's agent adoption, aligns revenue with the trend the product bets on, and prices the thing that actually creates the governance burden.

---

### 014 — Brand and UI are designed with Impeccable, in Operate mode

**Decision.** Visual identity and UI/UX are produced through the Impeccable skill (`init` → `new-work` → `colorize`/`typeset` → `extract`, then `shape` → build → `critique`/`audit` → `onboard` → `harden` → `polish`) rather than assembled from component-library defaults. Every product surface is designated **Operate** mode.

**Why.** Operate mode is the correct designation — the user is completing a task, so scanability, consistency and native expectations outrank expression. Naming the mode explicitly prevents a marketing aesthetic leaking into an approval queue. Running `init` early means later design work starts from real product truth rather than reconstructing it.

**Consequence.** Colour must be treated as semantic and safety-critical, not decorative: policy outcomes, trust tiers, run states and human-vs-agent actor type are all colour-encoded, so state may never be conveyed by colour alone. A monospace family is first-class, since ARNs, run IDs, logs and diffs are primary content. The product name becomes a blocking prerequisite for brand work.

---

### 015 — One semver for the whole monorepo, not per-service versions

**Decision.** A single version applies to every release and every image tag (`controlplane`, `worker`, `mcp`, and later `frontend`) — not independent versions per service, despite `semantic-release` supporting that mode.

**Why.** [PLAN.md](PLAN.md) already accepts version sprawl as a cost of self-hosting: customers run whatever they installed, and several versions need simultaneous support. Per-service versioning would add a compatibility matrix on top of that — "does worker 2.1 talk to control-plane 1.9?" A self-hosted customer, and whoever supports them, should be able to name one version as a complete description of what's deployed.

**Consequence.** A docs-only change to the frontend bumps the same number as a broker security fix. Accepted: the versioning scheme should not undermine the same legibility the product exists to sell.

---

### 016 — Trust tiers are named `ReadOnly` / `HumanInTheLoop` / `Autonomous`, in that order

**Decision.** Tiers are renamed from environment-flavoured labels (`ReadOnly`/non-prod/prod) to autonomy-flavoured names, and — critically — **`Autonomous` outranks `HumanInTheLoop`**, not the reverse.

**Why.** An earlier draft ordered them `ReadOnly(1) < Autonomous(2) < HumanInTheLoop(3)`, reasoning that `HumanInTheLoop` reaches the highest-stakes (production) actions. That was a real bug, not a style issue: the privilege-escalation check (`Issue` refuses to grant a tier higher than the issuer's own) would then let a `HumanInTheLoop` actor — one that never acts without a human checking it — mint an `Autonomous` agent that acts with **no check at all**. That launders supervised trust into unsupervised trust, which is exactly what the recursive-delegation ban in [AGENT-MODEL.md](AGENT-MODEL.md) exists to prevent, and the ordering let it straight through.

The corrected model treats tier as a **ceiling of unsupervised trust**, not a ceiling of consequence reached: fully autonomous, auto-rollback continuous deployment is a *more* mature, more trusted state than manual-approval-gated deployment, not a lesser one — so `Autonomous(3) > HumanInTheLoop(2) > ReadOnly(1)` is the correct order on both the security argument and the DevOps-maturity argument.

**Consequence.** `pkg/identity.Tier` constants and their doc comment were corrected, along with every doc reference (`SECURITY-MODEL.md`, `AGENT-MODEL.md`, `SCOPE.md`, `ONBOARDING.md`, `V1-ROADMAP.md`). Environment risk stays a separate, independent scope (`Claims.Environments` plus a per-environment policy setting) — a tier is never named after an environment.

---

### 017 — Cloud credential broker is a generic interface; only AWS ships in v1

**Decision.** `worker/internal/broker` becomes `worker/internal/broker/aws`, implementing a new provider-agnostic `pkg/cloud.Broker` interface. GCP and Azure implementations remain deferred — the interface exists now, the code behind it does not.

**Why.** Raised as "should v1 support Azure/GCP too, since Phase 0's issue is AWS-STS-specific." The two providers don't share AWS's `sts:AssumeRole` + external ID primitive: GCP uses Workload Identity Federation with service account impersonation, Azure uses Entra ID federated credentials with MSAL, each with its own scoping mechanism (session policies vs. IAM conditions vs. subject-claim matching) and its own version of the EKS-access-entries problem that motivated decision 006 (GKE Workload Identity bindings, AKS federated credentials). Implementing both properly — including a real sandbox account per provider to test credential minting — is roughly 2–3x the Phase 0/1 broker and onboarding work, not a linear extension, and works directly against the roadmap's stated priority of reaching a Phase 1 demo quickly. No design partner or use case currently requires GCP or Azure.

Generalising the interface now costs almost nothing beyond what Phase 0 already requires — the same reasoning as decision 008's `pkg/ci.Adapter` — and converts a future multi-cloud push from a rewrite into an additive `broker/gcp`/`broker/azure` package.

**Consequence.** `pkg/cloud` must stay free of AWS-specific assumptions (ARNs, session policies) in its interface shape. Revisit real GCP/Azure implementation only when a specific customer need exists.

---

### 018 — Admin bootstrap is operator-chosen, not a generated token

**Decision.** There is no static admin bootstrap token, and the control plane never generates or delivers an initial admin credential itself. A fresh, empty deployment exposes a `POST /v1/setup` endpoint (plus a cheap `GET /v1/setup` status check) that works exactly once, guarded by "does any tenant already exist" — the operator's own request supplies the tenant name, admin username and password directly, in that one call, and that endpoint refuses permanently afterward. `idpctl setup` is the CLI wrapper that prompts for these interactively and logs the operator in immediately; a future Phase 3 UI is a second client of the identical endpoints, not a different mechanism.

**Why.** Every alternative considered has a real, documented failure mode:

- **A plain env var the operator invents in advance** puts the value in the long-running server process's own environment table for its entire lifetime (readable via `/proc/<pid>/environ`), and asks the operator to choose a secret before they have any context for what it protects.
- **Printing a generated secret to stdout/logs** risks being captured and retained by whatever log aggregation pipeline the customer already runs, with no way for us to know or control that.
- **A shared, fixed default credential** (`admin`/`admin`) is public knowledge the instant it ships, because this repo is BSL-licensed open source — every deployment is vulnerable to the same known credential from the moment it's network-reachable until someone happens to change it. This is the exact failure mode behind the Mirai botnet and repeated real-world Grafana exposures.
- **Writing the generated secret to a file we manage**, or **a Kubernetes-Secret-API-based flow** (the pattern Argo CD uses: auto-generate, store in a Secret object, retrieve via `kubectl`), both work, but the latter is Kubernetes-specific — this product is explicitly deployable via docker-compose, Kubernetes, Nomad, or bare systemd, and a bootstrap mechanism tied to one orchestrator's API contradicts that.

The pattern that has none of these problems is the one mature self-hosted software (Nextcloud, and most first-run setup wizards) already converges on: nothing is generated at all, so there is nothing to deliver, log, store, or leak. The operator's first interaction with a fresh instance sets their own real credential directly.

**Consequence.** `local_users` is a small table separate from `actors` (most actors — every agent — never have a local password at all; only this one bootstrap human does, until OIDC lands in Phase 2). Trust for that one-time window is established by "who reaches the instance first," the same accepted tradeoff every tool using this pattern lives with — documented operational guidance is not to expose a fresh instance publicly before completing setup. `docs/ONBOARDING.md`'s "Bootstrap produces a single static admin token" line is stale and is corrected in the same change as this decision.

The same principle extends to the control plane's own JWT-signing key: it is generated once and persisted in Postgres (`signing_keys`, a singleton row) on first startup, never supplied by the operator as an env var. This isn't a human credential — nobody ever needs to know or type it — so it doesn't carry the delivery problem the admin password does; it only needed applying the same "don't ask the operator to invent a secret before first boot" discipline. It also isn't a cloud-credential-storage violation: unlike an AWS credential, this key only affects the control plane's own session integrity, not the customer's cloud account, so it doesn't touch the "dumping the database yields nothing usable against the customer's cloud" invariant.

---

### 019 — The worker authenticates with its own credential, not an `identity.Claims` token

**Decision.** The worker does not get a third `identity.ActorType`. It authenticates to the control plane with a purpose-built, opaque bearer credential (`worker_credentials`: hashed at rest, scoped to a set of environments), checked by its own middleware, entirely separate from `identity.Issuer`/`Verifier` and the `Claims`/`Tier`/`Team`/`Delegation` model that humans and agents use.

**Why.** `identity.Claims` exists to answer "how much unsupervised authority does this principal have to *decide and take* an action, and on whose behalf." A worker never decides anything — it polls for jobs another actor already authorised at a tier already checked, executes exactly that, and reports facts back. Forcing it through the actor model would mean inventing answers to questions that don't apply to it: what `Tier` does infrastructure hold (`Autonomous` describes an actor trusted to act unsupervised, not a job runner with no discretion at all)? What `Team` does it belong to (`actors.team_id` is `NOT NULL`, and "team" is an organisational concept for humans and agents, not infrastructure)? Whom is it acting "on behalf of" for the delegation chain the privilege-escalation rule depends on? Stretching the actor model to cover this would make `Tier`/`Team`/`Delegation` mean two different things depending on which kind of actor you're looking at, which is precisely the kind of ambiguity the privilege-escalation and delegation rules in [SECURITY-MODEL.md](SECURITY-MODEL.md) depend on *not* having.

The worker's actual trust question is flatter and narrower: "is this the registered worker for one of these environments?" — service-to-service authentication, not actor authorisation. A separate, purpose-built credential answers exactly that question and nothing more, the same way `local_users`/`controlplane/internal/credential` is already a separate, narrower mechanism from agent tokens for the same reason (decision 018).

**Consequence.** Two authentication mechanisms exist for two genuinely different trust questions: `identity.Claims` (verified JWT, `Tier`/`Team`/`Delegation`) for actors that take governed actions, and `worker_credentials` (hashed opaque bearer token, environment-scoped, no tier or delegation) for the worker polling for jobs and reporting results. `requireAuth` (backed by `identity.Verifier`) and a new `requireWorkerAuth` (backed by a hash lookup) are separate middleware, each gating a disjoint set of endpoints — never the same route accepting either. See [SECURITY-MODEL.md](SECURITY-MODEL.md)'s "Worker authentication" section for the mechanism itself.

---

### 020 — One worker identity serves many AWS accounts; account count never dictates worker count

**Decision.** A single worker process, running under one stable identity (an IRSA role, an EC2 instance profile, an ECS task role), assumes IAM roles across as many customer AWS accounts as it has been granted `Environment`s for. Worker count is driven by operational concerns (throughput, HA, CI reachability — see decision 021), never by how many AWS accounts a customer has.

**Why.** `sts:AssumeRole` is inherently cross-account: the calling identity and the target role never need to share an account, only a trust policy naming the caller as principal plus a per-target external ID — the same hub-and-spoke pattern Terraform Cloud, Spacelift and env0 already use to manage many customer accounts from one runner identity. Nothing in this codebase's broker or schema assumes otherwise: `worker/internal/broker/aws.Broker` is a stateless STS adapter taking `cloud.EnvironmentConfig` fresh on every call, and `worker_credential_environments` is already a many-to-many join between a worker credential and the `Environment`s it may serve. A customer with 1,000 AWS accounts deploying 1,000 workers to match would make this product unusable at exactly the scale it should be winning.

**Consequence.** Onboarding a new AWS account never requires touching the worker: it's a Terraform apply in the new account plus one control-plane call granting that `Environment` to an existing worker credential (see [WORKER-AWS-AUTH.md](WORKER-AWS-AUTH.md) for the IAM mechanics). Docs that read as "one worker per account" (the pre-020 phrasing in [ARCHITECTURE.md](ARCHITECTURE.md), [ONBOARDING.md](ONBOARDING.md) and [SECURITY-MODEL.md](SECURITY-MODEL.md)) were narrating the single-environment quickstart, not stating an architectural limit, and are corrected in the same change as this decision.

---

### 021 — The worker supports self-hosted and SaaS CI as equally first-class placements

**Decision.** The worker's OIDC-callback listener (`internal/ciauth`, Phase 1) must work whether the customer's CI runs self-hosted (their own runner fleet) or as a SaaS/cloud-hosted provider (GitHub-hosted runners, Bitbucket Cloud Pipelines). Neither is the default case with the other bolted on afterward, and a single customer may run both at once against the same control plane.

**Why.** Self-hosted CI can share a private network with the worker, so the callback stays internal — this is the case the existing docs describe. But most customers, and certainly the larger ones, run CI on the provider's own infrastructure, where there is no private network for the worker to be "inside" of. Designing only for the self-hosted case would silently exclude the common case; treating SaaS CI as a variant to retrofit later risks baking network-locality assumptions into `internal/ciauth`'s first implementation that are expensive to unwind.

**Consequence.** Under self-hosted CI, the callback boundary stays an internal one, as already documented. Under SaaS CI, the same callback endpoint must be externally reachable — a public-facing boundary, terminated over TLS, with the same trust assumption as any other public webhook receiver (Codecov, Snyk) rather than a different one. This is a placement and design requirement to build `internal/ciauth` against from the start in Phase 1; it does not itself implement the callback, which remains unbuilt (`internal/ciauth` is currently a `.gitkeep` stub).

---

### 022 — Worker self-registration is gated by a shared bootstrap secret, never by the control plane's own session auth

**Decision.** `POST /v1/workers/bootstrap` lets a worker mint or rotate its own credential by presenting a shared secret (`WORKER_BOOTSTRAP_TOKEN`/`_FILE`, config only, never stored in the database) instead of an authenticated human session. It can only ever produce a credential scoped to **zero environments**. Granting it real environment access still requires a separate, authenticated Autonomous-tier human call to `POST /v1/workers/{id}/environments`.

**Why.** The old path — `idpctl setup`, then `idpctl worker enrol`, then hand-carrying the resulting token onto the worker — means a customer cannot bring up a multi-service deployment (control plane, worker, everything else) in one shot; the worker is stuck until a human runs two more commands. A shared secret generated once by the deployment tooling itself (a Helm `Secret` template, a compose `.env`) and handed to both the control plane and the worker removes that step, the same pattern Kubernetes join tokens and cluster-formation cookies (Consul, RabbitMQ) already use. It's safe specifically because of what it's constrained to produce: even a leaked bootstrap secret can only ever mint an inert, zero-scope worker identity — it has no path to `sts:AssumeRole` on anything, since that requires a separately-authenticated human to attach a real `Environment` first. This is a materially narrower trust boundary than `POST /v1/setup`'s (which is why that endpoint stays one-time-only and this one does not: it never mints authority, only an identity capable of asking for some later).

**Consequence.** Two ways now exist to end up with a worker credential — a human running `idpctl worker enrol` with at least one environment already in hand, or a worker self-registering with zero and being granted environments afterward — and both produce the exact same `worker_credentials` row shape, verified by the same `requireWorkerAuth`. Audit distinguishes them by action string (`worker.enrolled` vs. `worker.self_registered`), the latter attributed to the tenant's root actor since no human is behind the call. Rotation on every bootstrap call (rather than a cached token) means two worker replicas must never share one name.
