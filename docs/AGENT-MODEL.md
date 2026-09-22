# Agent Model

How agents come to exist in this platform, how they authenticate, and what they can do.

## We mint identities; we do not spawn agents

The user already has an agent — Claude Code, Cursor, Devin, Copilot, or something custom. This platform issues that agent an **identity** and governs what it can do. It does not run the agent.

**What we never do:**

- Run or host a model
- Hold the customer's Anthropic, OpenAI or other provider keys
- See prompts, completions or conversation content
- Pay for or broker inference
- Own the agent's reliability or behaviour

"Spawning an agent" here means **registering an identity and minting a credential**, not starting a process. The agent's intelligence is the customer's; its *authority* is ours to govern.

The alternative — hosting the agent loop, orchestrating a model, a "run agent" button — is a fundamentally different product (Devin, Factory territory). It would mean holding model keys, sandboxing the agent loop, and owning agent behaviour. It contradicts the [scope boundary](SCOPE.md) directly and is out.

## Enrolment

Three equivalent paths. All go through the **same internal service layer** — consistent with the one-governed-code-path principle in [ARCHITECTURE.md](ARCHITECTURE.md). None is a privileged back door.

### UI (primary for humans)

Agent enrolment is a first-class UI workflow, not a CLI-only capability. The person authorising an agent is often a team lead or platform admin who does not live in a terminal, and enrolment belongs next to the approvals and audit surfaces where governance already happens.

The UI flow captures: name, owning team, trust tier, permitted environments, and an expiry. It shows what authority is being granted in plain language before confirming — the granting human should not have to infer what `Autonomous` means from a bare number. See [SECURITY-MODEL.md](SECURITY-MODEL.md) for the tier names and why they describe autonomy rather than environment.

### CLI

```bash
idpctl agent create \
  --name claude-code-rhys \
  --team platform \
  --tier autonomous \
  --environments staging
```

### REST API

For programmatic enrolment — provisioning agents as part of a team's own onboarding automation.

### Who may enrol whom

**An actor may never grant an agent more authority than it holds itself.** A `HumanInTheLoop` human — one whose own actions always need a check — cannot mint an `Autonomous` agent that needs no check at all. Without this rule, enrolment becomes a trivial privilege-escalation path: supervised trust laundered into unsupervised trust.

**Agents may not enrol agents** by default. Recursive delegation launders authority: if agent A can mint agent B, the delegation chain becomes a place to hide rather than a record. If this is ever needed, it must be an explicit, separately-granted capability with the chain preserved in full.

## Identity and session are two levels

A registered agent is long-lived. The credentials it uses are not.

```
Agent identity  (long-lived, registered, revocable, billable)
      └── Session token  (short-lived, derived, per-session)
```

Why both:

- **Attribution.** A single long-lived token tells you *the agent* did something. A per-session token tells you *which session* — which is what you need when reconstructing an incident.
- **Revocation granularity.** Kill one compromised session without disabling the agent everywhere.
- **Blast radius.** A leaked session token expires on its own.

This mirrors how OAuth separates a client from its access tokens, and matches the prevailing pattern for agent credentials: ephemeral tokens scoped to a task, expiring in minutes rather than months.

## Delegation

Every agent token carries a **cryptographically bound delegation claim** identifying the authority it acts under. This is read from the verified token and never from a request body — see [SECURITY-MODEL.md](SECURITY-MODEL.md).

### Interactive agents

A human is present. Rhys runs Claude Code; the agent acts under Rhys's authority. The audit record reads *"claude-code-rhys, acting for Rhys"* — both facts preserved, neither collapsed into the other. This dual attribution is the core of what the product sells.

### Headless agents

An agent on a schedule, or one reacting to a PR event, has no human present when it acts. The delegation chain then points at the **authorising act** — the human or team who registered it and set its tier — rather than a live human.

This is the case where "who approved this" gets murky, so it is worth stating plainly: a headless agent's authority derives from its enrolment, and the enrolment record is therefore a governance artifact in its own right. Headless agents should carry shorter expiries and narrower environment scopes precisely because no one is watching.

## Token lifecycle

| Stage | Behaviour |
|---|---|
| Issue | Minted at enrolment with bound delegation claim, tier, environment scope and expiry. The mint is audited, recording the granting principal |
| Deliver | See below — never copy-paste if avoidable |
| Rotate | Session tokens are short-lived and renewed against the identity |
| Revoke | Individually, per session or per identity, taking effect immediately |
| Expire | Every identity carries an expiry. Indefinite agent credentials are not offered |

### Delivering the token securely

Copy-pasting a token from a UI is the familiar pattern (GitHub PATs), but it ends with secrets in config files and occasionally in Git.

**Preferred: a device-style enrolment flow.** `idpctl agent login` prints a short code; the authorising human approves it in the UI; the CLI receives the token and writes it to a credential store. No secret transits a clipboard.

**If a token is shown in the UI**, show it once, and make the reference pattern explicit in the surrounding copy: MCP configuration should reference an environment variable, never embed the literal token in a file likely to be committed.

## How the agent actually talks to us

### MCP (primary)

The agent points at our MCP server and calls governed tools. This is the natural surface — the agent uses it as a tool rather than as an integration.

**The exposed tool list reflects the agent's tier.** A tier-1 agent does not see `deploy` among its available tools at all. This is better than allowing the attempt and denying it: it saves wasted model turns, and it stops the agent reasoning about paths it cannot take.

Tools are async — they return a run ID and poll. An agent cannot hold a forty-minute tool call.

### CLI

Any agent that can run a shell can use `idpctl`. This matters for sequencing: Claude Code can drive the governed path from Phase 1, before the MCP server exists, which means the product is dogfoodable immediately.

### REST

Direct API access for custom agents and integrations.

All three converge on the same service layer and the same policy checks. There is no path that skips governance.

## Several agents, one human

Expected and supported. Rhys may have Claude Code locally at `Autonomous` for staging, and a CI-triggered remediation agent at `ReadOnly`. Distinct identities, distinct tokens, distinct audit trails, both delegating to Rhys. The model working as intended rather than an edge case.

## Metering

[Decision 013](DECISIONS.md) prices per agent identity, so **enrolment is the billing event**. The commercial meter and the governance primitive are the same object, which is a good sign the pricing axis is the right one.

## Open questions

- **Session token TTL.** Short enough to limit a leak, long enough not to interrupt a working agent mid-task. Needs a real number informed by how long agent sessions actually run.
- **Enrolment approval.** Should minting an `Autonomous` agent itself require a second approver, given it carries the most unsupervised trust in the system? Arguably yes, but it adds friction at exactly the moment someone is trying the product.
- **Agent discovery.** When an agent is registered, should it be able to enumerate its own permitted environments and actions, or should that be pushed to it? Discovery is friendlier; pushing is tighter.
