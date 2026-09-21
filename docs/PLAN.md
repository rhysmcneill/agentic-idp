# Product Plan

## Problem

AI coding agents increasingly build, test and deploy real software. The platforms that govern *how* that happens were designed for humans:

- **Service catalogs** (Backstage, Port, Cortex) assume a human clicking a UI. Agents are an afterthought.
- **IaC governance platforms** (Spacelift, env0, HCP Terraform) enforce policy well, but treat an agent as just another API caller — no identity, no trust tier, no delegation chain.
- **Agent-identity vendors** (Descope Agentic Identity Hub, Arcade.dev, WorkOS, Agyn, Dock/Truvera) issue scoped credentials but have no catalog and no infrastructure execution.
- **Agent sandboxes** (E2B, Modal, Runloop) isolate code execution but have no organisational governance.

Teams therefore assemble four vendors and glue them together.

## Competitive position — stated honestly

**No single product does this end to end.** But every individual layer is well served, and the category has a name and a shortlist already.

This means the play is **collapsing a four-vendor stack, not inventing a category.** That is a harder pitch than greenfield, and it demands the product be clearly better than glue rather than merely present. Assume prospects have already assembled something that works.

What remains genuinely unclaimed is the *synthesis*: catalog + agent identity + policy + execution in one governed path, where the trust tier is enforced all the way down to the cloud credential.

## Solution

One governed path from intent to infrastructure.

Every actor — human or agent — is a typed principal with a cryptographically-bound identity, a trust tier, and scoped self-service access to a catalog of actions. Policy, approval, audit and cost attribution work identically for people and machines. Execution runs through a customer-hosted worker that delegates to pipelines they already trust.

The differentiator is that a trust tier is not a UI convention. It is enforced at the AWS credential level, and it survives across the CI boundary.

## Business model

**They buy the software and host it themselves.**

Why self-hosted first:
- The product brokers cloud credentials. "Runs in your account, we never see your credentials" is a far easier first sale than asking a company to trust a young vendor's control plane with cross-account production access.
- **No SOC 2 burden early** — that is $20–50k and several months, and would be required before the first serious hosted customer.
- No infrastructure cost, no 24/7 on-call, no incident response for a small team.

Known costs of this model, accepted deliberately:
- **Version sprawl.** Customers run whatever they installed; several versions will need support.
- **No feedback loop** unless telemetry is built in. Opt-in phone-home from the start — retrofitting it later reads as invasive.
- **Procurement cycles.** Self-hosted usually means enterprise sales — contracts, security review, legal — not self-serve signup.

A hosted offering may follow. It is the same binary with the same schema (`tenant_id` exists from commit one), not a second product.

## Pricing

**The meter is agent identities.** Human seats are cheap or free; agent identities are what is counted.

This is on-thesis: it grows as the customer's agent adoption grows, so revenue tracks the trend the product is betting on, and it prices the thing that actually creates the governance burden.

## Licensing

**BSL 1.1**, single repository, Change Date four years after each release converting to Apache 2.0, with an Additional Use Grant making non-production use free.

Reasoning:
- **Pure proprietary is not viable** — platform teams will not run a closed binary that brokers production cloud credentials. Source visibility is close to mandatory here.
- **Apache 2.0 everywhere has no revenue mechanism** for a self-hosted product. It would mean zero revenue until a hosted offering exists.
- **Open-core** (Apache core + proprietary features) works, but the feature boundary has to be drawn and defended from commit one, with no customers to guide where it belongs.
- BSL is one codebase and one licence — materially less operational work for a small team.

**A CLA is required before the first external PR.** This matters more under BSL than Apache: as licensor, selling commercial licences covering the whole codebase — or later extracting contributor-written code into an Apache library — needs rights that a DCO sign-off alone does not grant.

**Extraction plan**: around Phase 1–2, extract the agent identity and audit primitive into a separate Apache 2.0 library. That provides the open-source top-of-funnel without committing the platform. Doing it later rather than now means the boundary is discovered from working code instead of guessed at. Note the asymmetry that drives this ordering: restrictive → permissive is easy, permissive → restrictive is effectively impossible.

## Go-to-market

Devtools in this niche sell through technical content and community, not paid acquisition.

- **Open-source primitive as top-of-funnel** — the extracted identity/audit library, genuinely useful standalone.
- **Technical writing** on the specific problem (agent identity, blast-radius control, attribution) — not generic AI-and-DevOps commentary. Hacker News, dev.to, LinkedIn.
- **Existing communities**: Platform Engineering Slack/Discord, CNCF/Backstage, r/devops, agent-builder communities whose users will need guardrails once they reach production.
- **Warm network first.** Infrastructure colleagues and past colleagues convert far better than cold outreach.
- **Design partners before build.** If three to five companies will not commit to weekly feedback in exchange for early access, that is signal to revisit the wedge before investing months.
