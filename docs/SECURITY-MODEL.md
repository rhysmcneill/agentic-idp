# Security Model

The security model *is* the product. A customer's security review will ask for this document; writing it before the code exists is how design holes get caught while they are still cheap.

## Trust boundaries

| Boundary | What crosses it | What we assume |
|---|---|---|
| Agent → control plane | Agent token | Token is verifiable; **its claims are trusted, the request body is not** |
| Control plane → worker | Job instructions | Worker authenticates with its own hashed bearer credential, scoped to environments — not an actor token; connection is outbound-only from the worker |
| Worker → AWS | `sts:AssumeRole` | Role and external ID were registered by an authorised human |
| Worker → CI | Trigger + correlation ID | CI is customer-operated and semi-trusted |
| CI → control plane | CI OIDC token | Token is verified against the provider's JWKS; **claims decide, not assertions** |

The recurring rule: **anything crossing a boundary where the other side could forge it must never feed authorisation.**

## Agent identity and delegation

An agent acting "on behalf of" a human is the core audit claim of the product. It has to be real.

**The flaw to avoid:** modelling `delegated_by` as a field on the request or actor object. If an agent can assert its own delegation chain, the chain is self-reported and the entire audit story is worthless.

**The design:** delegation is a **claim cryptographically bound inside the agent's token**, issued at mint time by the authorising human or team. The control plane reads it from the verified token. A `delegated_by` value in a request body is ignored.

Agent tokens are:
- Short-lived
- Scoped to tenant + trust tier + an explicit environment set
- Individually revocable
- Audited at mint, with the granting principal recorded

## Trust tiers

Tiers are not a UI convention. They are enforced at the AWS credential level.

A tier is **not an environment.** Naming a tier after one (an earlier draft used "non-prod"/"prod") would conflate two independent things: how much authority a tier carries, and which environments an actor may act *in* at all (a separate scope on its token, checked independently — a given environment can also carry its own risk setting requiring approval regardless of tier).

The tier scale is a **ceiling of unsupervised trust**: higher means more trusted to act without a human checking each action — the same way fully autonomous, auto-rollback continuous deployment is a more mature, more trusted state than manual-approval-gated deployment, not a lesser one. `Autonomous` outranks `HumanInTheLoop` because it needs no per-action check at all; `HumanInTheLoop` is the intermediate, still-supervised state.

| Tier | Name | Authority |
|---|---|---|
| 1 | `ReadOnly` | No action authority; inspect catalog, runs, audit |
| 2 | `HumanInTheLoop` | May propose; a human approves **each action before** it executes |
| 3 | `Autonomous` | Acts unattended within its granted environments — no pre-approval, but monitored and revocable after the fact |

Realistically agents sit at `ReadOnly`/`HumanInTheLoop` for production-adjacent work; `Autonomous` is reserved for well-proven, low-risk paths.

This ordering is also what makes the privilege-escalation rule (an actor cannot mint one with more authority than itself — see [AGENT-MODEL.md](AGENT-MODEL.md)) actually prevent something meaningful: a `HumanInTheLoop` actor, which never acts without a human checking it, must not be able to mint an `Autonomous` agent that acts with no check at all. That would launder supervised trust into unsupervised trust — the reverse ordering would have permitted exactly that.

## Role per trust tier — not session policies

Credentials are scoped by using **a distinct IAM role per tier**, not one role narrowed by session policies. Four reasons:

1. AWS session policies are capped at roughly 2048 characters packed — insufficient for rich scoping.
2. Session policies can only *restrict*, never grant.
3. Role chaining caps session duration at one hour.
4. **Decisively: EKS access entries map the _role_.** One role across tiers means every tier lands as the same Kubernetes identity, and K8s RBAC cannot distinguish an agent from a human. Distinct roles fix this cleanly.

## Credential handling

- The control plane **never holds customer cloud credentials.** It holds role ARNs, external IDs and a trust-anchor principal. Nothing assumable on its own.
- The worker calls `sts:AssumeRole`, holds the result **in memory only**, uses it, and discards it. Never written to disk or database.
- Every mint is an audited event recording the actor, the action it was minted for, the granted scope and the TTL.
- Cross-account trust uses an **external ID per environment** — standard confused-deputy mitigation.
- The trust anchor is **configuration, not hardcoded**: self-hosted uses the customer's own instance identity (instance profile / IRSA); a future hosted offering would use our account ID with a mandatory external ID.

## Worker authentication to the control plane

The worker is not an actor. `identity.Claims` (`Tier`, `Team`, `Delegation`) answers "how much unsupervised authority does this principal have to decide and take an action, and on whose behalf" — a question that doesn't apply to the worker, which never decides anything. It only polls for jobs another actor already had authorised, executes exactly that, and reports facts back. See [Decision 019](DECISIONS.md).

Its actual trust question is narrower and flatter: **is this the registered worker for one of these environments?** That's answered by its own mechanism, entirely separate from `identity.Issuer`/`Verifier`:

- `idpctl worker enrol --environments staging,prod` mints a high-entropy opaque bearer token, shown once, and stores only a SHA-256 hash of it (`worker_credentials`) — not bcrypt: unlike the admin's bcrypt-hashed password in `controlplane/internal/credential` (decision 018), a bearer token carries no separate identifier (a username) to find its row by first, so it needs a deterministic, directly-indexable hash. The token's own entropy stands in for a salt.
- A separate `requireWorkerAuth` middleware hashes the presented token and looks it up directly. It never touches `identity.Verifier`, and it gates only the worker-facing endpoints (poll for a job, report a result) — never a route `requireAuth` also serves.
- The credential carries an environment scope, not a tier: the worker can only claim and report on verification jobs for environments it was granted, mirroring `actor_environments`' shape without inheriting its actor semantics.
- That scope is a **set**, not a single environment: one worker credential can be granted many `Environment`s, including ones in different AWS accounts — a worker's authority is bounded by which environments it holds, never by how many AWS accounts exist. See [Decision 020](DECISIONS.md) and [WORKER-AWS-AUTH.md](WORKER-AWS-AUTH.md).

This keeps the two auth mechanisms honest rather than overloading one to mean different things for different callers: `identity.Claims` for actors that take governed actions, `worker_credentials` for the worker proving it's the legitimate poller for a customer's environments.

## The CI boundary — the hole and the fix

**The hole:** most customers' CI already holds a broad OIDC role. If an agent can trigger that pipeline, the agent effectively inherits the pipeline's permissions regardless of its trust tier. The governance model leaks entirely at the CI boundary, and every guarantee above becomes decorative.

**The fix:** the pipeline must not use its own static role. It calls back presenting its **CI OIDC token** (GitHub and GitLab both issue these with repo, workflow and ref claims). The callback is handled by the **worker**, not the control plane — brokering credentials means calling STS, and only the worker may do that. The worker:

1. Verifies the token against the provider's JWKS
2. Asks the control plane, over its existing outbound connection, whether the correlation maps to an approved run and at what tier
3. Confirms the run is still in an executing state
4. Mints credentials scoped to the **triggering actor's tier**, not the pipeline's own role

This inbound path's boundary depends on the customer's CI placement — see [Decision 021](DECISIONS.md), and both cases are equally first-class, not one a variant of the other:

- **Self-hosted CI**: the worker sits alongside the customer's own runners, so this inbound path is an internal boundary.
- **SaaS/cloud-hosted CI** (GitHub-hosted runners, Bitbucket Cloud Pipelines): the CI job runs on the provider's infrastructure, with no private network to share, so this inbound path is instead a **public-facing boundary**, terminated over TLS — the same trust assumption as any other public webhook receiver (Codecov, Snyk), not a weaker one.

Either way, the link that crosses a trust boundary — worker to control plane — remains outbound-only, and the control plane stays credential-free. That property matters most for a future hosted offering, where the control plane would sit outside the customer's boundary entirely.

**It must fail closed.** A pipeline whose run was denied, or which presents a correlation ID that does not match an approved run, receives nothing — it must not fall back to its own role. This is an explicit Phase 1 verification step.

**The tradeoff, stated plainly:** this requires the customer to modify workflows to fetch credentials from us rather than assuming their own role. That is the difference between "drop-in" and "rewire your CI", and it is the single biggest adoption risk in the v1 strategy. It is the first thing to put in front of a design partner.

## Break-glass

**Requirement, not an SLA.** There is no uptime obligation — customers operate the platform. But availability responsibility does not close the design question.

If the credential broker is the only path to deploy credentials, then when it is down the customer cannot deploy — **including the fix that restores it.** That is a circular dependency designed into their estate, and it is a reason not to adopt.

**The design rule:** tier IAM roles must remain **independently assumable by a documented emergency principal** that the customer's security team controls, audited separately from our run records. We must never design a credential path that forbids this.

Self-hosting makes this straightforward — it is their AWS and their IAM. The only requirement is not to build something that precludes it.

## Explicit non-protections

Stated so that reviews are honest and expectations are correct:

- **We do not sandbox executed code.** The worker runs inside the customer's own trust boundary, where they already accept this risk for CI. Terraform providers execute arbitrary binaries and `local-exec` runs shell.
- **We do not prevent out-of-band changes.** Someone with direct cloud access can still change things. We aim to *detect* that they did — see unattributed-change detection in [SCOPE.md](SCOPE.md).
- **We do not manage secrets.** Deferred deliberately.
- **We do not validate what the customer's pipeline does** once it has credentials, beyond scoping those credentials.

## Verification requirements

These are acceptance criteria, not aspirations:

- A forged `delegated_by` in a request body is ignored in favour of the verified token claim.
- A denied tier requesting credentials via the OIDC callback receives nothing and the pipeline **fails closed** rather than falling back to its own role.
- The control plane's database, dumped in full, contains nothing that can be used to access a customer's cloud account.
- Tier roles remain assumable by the documented break-glass principal with the control plane entirely offline.
