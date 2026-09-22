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
