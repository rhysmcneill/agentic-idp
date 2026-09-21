# Roadmap to v1

Sequenced so the hardest-to-copy pieces land first and can be validated with design partners before the broader surface is built.

A note on pacing: solo with no hard deadline still means validation speed matters. Phases 0–4 as scoped is comfortably a year of solo work. **Phase 1 is the earliest honest demo** — the goal is to reach it and put it in front of someone, not to complete Phase 4 in private.

---

## Phase 0 — Prove the trust model

Deliberately tiny. No OIDC, no catalog, no frontend.

- `Tenant`, `Team`, `Actor` models; **token-bound delegation claims**
- Agent enrolment via CLI and API (UI comes in Phase 3) — see [AGENT-MODEL.md](AGENT-MODEL.md)
- Static admin bootstrap token
- `Environment` registration: tier role ARNs, external ID, trust anchor
- `sts:AssumeRole` connectivity check
- Append-only audit log
- Worker skeleton: polls control plane, assumes role, reports back

**Milestone** — a mock agent carrying a bound delegation claim triggers a no-op job that a customer-run worker executes by assuming a real tier role.

**Verification**
- The control plane stores no credentials; dumping the database yields nothing usable against the customer's cloud
- A forged `delegated_by` in the request body is ignored in favour of the verified token claim
- Tier roles remain assumable by the break-glass principal with the control plane offline

---

## Phase 1 — Governance and first real execution

The differentiator. This is the phase to validate with design partners.

- Typed trust tiers; policy check on every action **and every credential mint**
- Run state machine and Postgres-backed job queue
- Approval workflow (CLI + Slack webhook)
- **GitHub Actions adapter only**, behind the generic `pkg/ci` interface
- CI OIDC callback: verify token, match correlation, broker tier-scoped credentials
- Per-run cost capture
- Opt-in telemetry

**Milestone** — demoable as *"governed agent access to the pipelines you already run."*

**Verification**
- Two tiers configured: auto-approve staging, production requires approval
- A human action and an agent action both traverse the full path correctly
- The pipeline receives **tier-scoped** credentials via OIDC callback
- **A denied tier fails closed** — the pipeline does not fall back to its own role
- Audit records attribute correctly, including the credential-mint event

**The question this phase exists to answer:** will customers accept rewiring a workflow to fetch credentials from us? That is the adoption risk, and no amount of further building resolves it.

---

## Phase 2 — Agent-native surface and catalog

- MCP server with async request/poll semantics
- Service catalog and ingestion mechanism
- `entity ↔ environment ↔ pipeline` binding
- Scaffold-new-service action
- OIDC for human authentication
- OPA as the custom-policy escape hatch
- Extract the Apache 2.0 identity/audit library

**Milestone** — an agent completes a PR-based change end to end via MCP: propose → policy check → human approves diff → merge → pipeline applies, fully reconstructable from the audit log.

---

## Phase 3 — Brand, UX and breadth

**Gate before starting** — a design partner connects their own sandboxed AWS account and runs the Phase 1 flow. If the credential and trust model does not feel obviously safe to them, stop and fix that before building UX.

Design and brand work is driven by the [Impeccable](../.claude/skills/impeccable/SKILL.md) skill and translated directly into code, rather than assembled ad hoc from component-library defaults.

---

### 3a. Brand and visual identity

**Prerequisite: the name.** `agentic-idp` is a working placeholder. A wordmark, palette and voice cannot be built around it, and launch content needs it too — so naming is now on the critical path rather than a later nicety. Expect the name to fall out of how design partners describe the problem back to you.

Deliverables:

| Deliverable | Notes |
|---|---|
| Name and wordmark | Blocking everything else in this section |
| Mark / logo | Must survive a 16px favicon and a monochrome terminal context |
| Colour system | Semantic before decorative — see below |
| Typography | Including a first-class monospace — see below |
| Voice and tone | Serious and precise. This product holds production cloud credentials; playful undermines the sale |
| Dark mode | Table stakes for a developer tool, not a follow-up |

#### Colour is safety-critical here, not decorative

Unusually for a product palette, colour in this UI carries meaning that has consequences if misread:

- **Policy outcomes** — allow / deny / needs-approval
- **Trust tiers** — 1, 2, 3, escalating legibly so production reads as highest caution
- **Run states** — succeeded / failed / cancelled / timed-out
- **Actor type** — human vs agent, a core product concept that needs a consistent visual language of its own. Most design systems never have to distinguish actor types; this one does, everywhere

Two hard rules follow:

1. **Never encode state in colour alone.** Icon, shape or text must carry it redundantly. This is an accessibility requirement, and here it is also a safety one — misreading "denied" as "approved" is a failure mode, not an inconvenience.
2. **Diff rendering needs deliberate treatment.** Red/green additions and deletions are the classic colour-blindness failure, and the diff is the surface where a human approves a production change. It must survive deuteranopia and greyscale.

#### Typography

A **monospace family is first-class, not an afterthought.** Role ARNs, run IDs, correlation IDs, logs, YAML and diffs are primary content, not code snippets embedded in prose. Choose it with the same care as the UI face, and check ambiguous glyphs — `0`/`O` and `1`/`l`/`I` confusion in an ARN or an account ID is a real operational hazard.

#### Impeccable commands for this section

`init` → `new-work` (establishes the visual world and writes `DESIGN.md`) → `colorize` → `typeset` → `extract` (tokens and components into a reusable design system).

---

### 3b. UI design and build

**Mode: Operate.** Per Impeccable's mode taxonomy, every surface here is Operate — the visitor is completing a task, not being persuaded. Scanability, consistency and native expectations outrank expression; brand lives in precise details. `reference/operate.md` carries the deeper guidance. Do not let a marketing-site aesthetic leak into an approval queue.

| Step | Command | Output |
|---|---|---|
| 1 | `impeccable shape` | UX and IA planned per surface, before any code |
| 2 | *build* | React + TypeScript surfaces |
| 3 | `impeccable critique` + `audit` | UX review with heuristic scoring; a11y, performance, responsive |
| 4 | `impeccable onboard` | First-run flows and empty states |
| 5 | `impeccable harden` | Error states, edge cases, i18n |
| 6 | `impeccable polish` | Final pass before shipping |

#### Surfaces, in priority order

1. **Approval queue with diffs** — the highest-stakes surface in the product. A human approving a production change is making a safety decision, often under time pressure. **Diff legibility is a safety property, not an aesthetic one**: if the approver cannot see what is changing at a glance, the governance model has a human-factors hole that no amount of policy enforcement closes. Design this first and hardest.
2. **Audit and attribution views** — dense, high-volume tabular data. `layout` and `typeset` carry most of the weight; the job is making "who did what, under what authority" reconstructable at a glance.
3. **Agent enrolment and management** — registering agents, setting tier and environment scope, rotating and revoking tokens. Enrolment is a **first-class UI workflow, not CLI-only**: the person authorising an agent is often a team lead who does not live in a terminal, and this belongs beside the approval and audit surfaces. The grant screen must state what authority is being given in plain language — nobody should have to infer what tier 2 means. See [AGENT-MODEL.md](AGENT-MODEL.md).
4. **Catalog browser** — what exists, who owns it, what can be done to it.
5. **Environment management** — role ARNs, tier configuration, connectivity status.
6. **Cost views** — per-run, rolling up per actor.

#### Why `onboard` matters more than usual

Day one, a freshly installed instance has an empty catalog, no runs, no agents and no environments. **The empty state is the first-run experience**, not an edge case — and for a self-hosted product with no onboarding call, it is the entire activation path. It should teach the [onboarding sequence](ONBOARDING.md) rather than render a blank table.

#### Why `audit` is commercially load-bearing

Accessibility is frequently a procurement requirement for enterprise buyers, particularly in the public sector and regulated industries. `impeccable audit` covers a11y alongside performance and responsive behaviour — treat its findings as sales blockers rather than polish.

---

### Run `impeccable init` early, not here

`init` captures durable product context into `PRODUCT.md`. There is no reason to defer it — the context already exists in [PLAN.md](PLAN.md), [SCOPE.md](SCOPE.md) and [ARCHITECTURE.md](ARCHITECTURE.md). Running it during Phase 0 or 1 means every later design command starts from real product truth instead of reconstructing it, and it costs nothing now.

---

### Non-UI work in this phase

- Unattributed-change detection (CloudTrail correlated against run records)
- Native Terraform and Kubernetes executors, **only if the integration ceiling actually binds**
- Optional Backstage plugin, so existing Backstage users adopt without migrating

---

## Phase 4 — v1 GA

- A design partner running Phases 1–2 against their real AWS account
- Documentation, with onboarding order-of-operations as the priority
- Packaging and licence enforcement
- Launch content built around the open-source primitive

---

## Deferred deliberately

Secrets management · multi-cloud · real-time cost attribution · microVM sandboxing · general drift detection · CI adapters beyond GitHub Actions

Each is deferred for a stated reason in [SCOPE.md](SCOPE.md) or [ARCHITECTURE.md](ARCHITECTURE.md). Deferral is a decision, not an oversight.

---

## Open decisions

**The product name.**
`agentic-idp` is a working placeholder. Naming now blocks brand identity (3a), and launch content needs it too — so it is on the critical path rather than a later nicety. Do not force it early: a good name usually falls out of how design partners describe the problem back to you, so revisit it after the Phase 1 conversations. *Needed before Phase 3.*

**Fixed actions or user-defined templates?**
Hardcoded `deploy` / `rollback` / `scaffold`, or a Backstage-scaffolder-style template system users define themselves. This is the difference between shipping a product and shipping a platform, and it materially changes the data model. *Decide before Phase 2.*

**Catalog ingestion mechanism.**
Repo scanning for `catalog-info.yaml`, API registration, or PR-based registration. *Decide before Phase 2.*

**Where the Apache library boundary falls.**
Deliberately unresolved — it should be discovered from working code rather than guessed at now. *Revisit at Phase 2.*
