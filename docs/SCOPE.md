# Scope

## The boundary

> **We govern and record actions. We do not author infrastructure, and we do not own desired state.**

This sentence is the test for every feature request. If a proposal requires us to author infrastructure or become the source of truth for desired state, it is out of scope regardless of how useful it sounds.

## In scope

- Actor identity for humans and agents, with cryptographically-bound delegation
- Trust tiers, and their enforcement down to the cloud credential
- Policy evaluation on every action and every credential mint
- Approval workflows, including showing the change diff to the approver
- A service catalog: what exists, who owns it, what can be done to it
- Triggering and tracking execution in systems the customer already runs
- Audit: a complete, reconstructable record of who did what, where, and under what authority
- Cost attribution per run, rolling up per actor
- Detecting changes that did **not** come through a governed run

## Out of scope

| Not ours | Why | Who does it |
|---|---|---|
| **General drift detection** | Needs state access and scheduled plans; unrelated to agent governance; in the delegate-to-CI model we may not even have state access | Firefly, Spacelift, env0, HCP |
| **Terraform state management** | Backends, locking, concurrency, workspaces. This is precisely why several companies exist | Spacelift, env0, Scalr, HCP |
| **Authoring infrastructure code** | The customer's IaC lives in the customer's repos, authored by them | The customer |
| **Being a CI system** | They have one. No CI means not yet a customer | GitHub Actions, GitLab, Jenkins |
| **Sandboxed code execution** | The worker runs inside the customer's trust boundary, where they already accept this risk for CI | E2B, Modal, Northflank |
| **Secrets management** | Deliberately deferred; large, well-served, orthogonal | Vault, AWS Secrets Manager |

### Drift vs. attribution — the distinction that matters

These sound similar and are different products:

- **Drift detection** — "does actual state differ from desired state?" Requires state access, scheduled plans, state-file parsing. **Not ours.**
- **Unattributed-change detection** — "did something change that did not come through a governed run?" Answerable from CloudTrail plus our own run records, with no state access at all. **Ours**, and a better fit — it is the audit story extended by one step, and it catches exactly the failure mode people fear about agents.

## Production is in scope

An IDP that stops at staging is a paved road that ends before the cliff. Developers step off the governed path exactly when the stakes are highest.

More importantly, **the thesis collapses without production.** Trust tiers are meaningless with nothing risky to tier against. Approvals are pointless if nothing needs approving. Credential scoping is not compelling if the blast radius is a test environment. Nobody is afraid of an agent breaking staging.

Two statements that are not in tension:

- **Does the platform govern production changes?** Yes, necessarily. That is where the value is.
- **Do agents deploy to production autonomously?** Almost never. Agents operate at tier 1–2 — unattended in staging. In production they may only *propose*; a human approves the diff.

Staging-only is a legitimate **go-to-market wedge** — land somewhere low-risk, let the audit trail earn trust, expand. The architecture assumes production from day one regardless.

## Responsibility split

| Thing | Who builds it | What we provide |
|---|---|---|
| Tier IAM roles per AWS account | **Customer** | A Terraform module they apply. We cannot create these — it would require credentials to obtain credentials |
| A pipeline that deploys their services | **Customer already has one** | Nothing |
| Pipeline fetching credentials from us | **Customer modifies one workflow** | A composite GitHub Action, ideally one `uses:` line |
| Catalog entries | **Customer writes, or we discover** | A `catalog-info.yaml` convention and repo scanning |
| Governance, audit, approvals, attribution | **Us** | The product |

The third row is the entire adoption friction, concentrated in a single diff. It is the thing to validate with the first design partner: it is the difference between "drop-in" and "rewire your CI".

## Availability

Customers operate the platform. There is **no SLA and no uptime obligation.**

This does not close the design question of what happens when it is down — see the break-glass requirement in [SECURITY-MODEL.md](SECURITY-MODEL.md). "They are responsible for uptime" is not an answer to "can they deploy the fix that brings it back up."
