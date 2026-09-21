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
