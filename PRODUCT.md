# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

React + TypeScript single-page app over the control plane REST API, with SSE for live approval updates (per docs/V1-ROADMAP.md Phase 3b). `frontend/` currently holds only a Dockerfile and an empty `src/`.

## Users

Two co-equal human audiences, since this is an internal developer platform:

- **Platform/security approvers** (platform engineers, team leads): approve or deny production-affecting actions requested by humans and AI agents, review audit, and enrol and scope agents. They work under time pressure, often outside a terminal.
- **Developers**: browse the catalog, trigger actions, follow their runs, see why something was denied or is waiting on approval, and enrol and manage the agents they delegate to.

AI agents are also actors on the platform but reach it through the CLI and MCP surfaces, not the UI.

## Product Purpose

An Internal Developer Platform where humans and AI agents build, test and deploy software through one governed, self-service catalog. Agent identity, trust tiers, policy, approval, audit and cost attribution are first-class and enforced identically for people and machines. Success: a developer can self-serve and see where their run stands without filing a ticket, and an approver can see who is asking, under what authority, and what will change, and decide safely.

## Positioning

Collapses a four-vendor stack (catalog, IaC governance, agent identity, sandboxes) into one governed path where the trust tier is enforced down to the cloud credential and survives the CI boundary. The control plane never holds customer cloud credentials; a customer-hosted worker does.

## Operating Context

Self-hosted: the control plane runs in the customer's infrastructure and a worker runs in their cloud account. Actors are typed principals (human or agent) with verified delegation claims. Work is asynchronous: runs are created, polled, approved and audited. Primary content includes role ARNs, account IDs, run and correlation IDs, logs, YAML and diffs.

## Capabilities and Constraints

- Trust tiers (ReadOnly, Autonomous, HumanInTheLoop), policy outcomes (allow / deny / needs-approval), run states, and actor type (human vs agent) are product concepts the UI must express.
- Surfaces in priority order: approval queue with diffs, audit and attribution views, agent enrolment and management, catalog browser.
- Pending approvals arrive without manual refresh (SSE).
- The UI is a thin client; no business logic in the frontend.
- Dark mode is required, not a follow-up.
- Product name is undecided (`agentic-idp` is a working placeholder).

## Brand Commitments

Name undecided; no wordmark, mark or palette exists. Voice is serious and precise: this product holds the trust boundary for production cloud credentials, so playful undermines the sale. Mark must survive a 16px favicon and a monochrome terminal.

## Evidence on Hand

No customers, testimonials, benchmarks or design-partner quotes exist yet; do not fabricate any. Real data comes from the control plane API (runs, approvals, audit, agents, cost).

## Product Principles

- Governance is legible: authority, actor and effect are readable at a glance, in plain language.
- Never encode state in colour alone; denied vs approved is a safety distinction.
- Monospace identifiers and diffs are primary content and must be unambiguous.
- Calm and precise over expressive; this is an Operate surface and marketing aesthetics must not leak in.

## Accessibility & Inclusion

Colour carries safety meaning, so state needs redundant icon, shape or text. Diff rendering must survive deuteranopia and greyscale. Ambiguous glyphs (`0`/`O`, `1`/`l`/`I`) in ARNs and IDs must be distinguishable.
