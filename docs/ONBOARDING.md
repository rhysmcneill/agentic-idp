# Customer Onboarding

> **Draft, written before implementation on purpose.** This document is a design tool as much as documentation: if the sequence below is awkward, that awkwardness is what kills trials. Revise the product to fix the sequence, not the other way round.

## Order of operations

The dependency chain is strict, and the first step exists because of a chicken-and-egg problem: we cannot create IAM roles in a customer's account, because doing so would require credentials we do not yet have.

```
1. Install control plane      (customer infrastructure)
2. Create tier IAM roles      (customer AWS — our Terraform module)
3. Register Environment       (role ARNs + external ID → control plane)
4. Deploy worker              (customer AWS account)
5. Verify connectivity        (worker assumes each tier role)
6. Create actors + teams      (humans, then agent identities)
7. Wire one pipeline          (add our composite Action to one workflow)
8. First governed run         (staging, tier 2, unattended)
```

Steps 1–5 are platform setup and happen once. Steps 6–8 repeat per team and per service.

---

## 1. Install the control plane

Helm into an existing cluster, or Docker Compose for evaluation. Requires PostgreSQL.

Bootstrap produces a single static admin token. OIDC is configured later (Phase 2); the admin token is the only credential at this point and should be treated accordingly.

## 2. Create tier IAM roles

Apply the module we ship, once per AWS account:

```hcl
module "agentic_idp" {
  source = "github.com/<org>/agentic-idp//deploy/aws"

  environment_name = "staging"
  trust_anchor     = "arn:aws:iam::<their-account>:role/<worker-role>"
  external_id      = "<generated per environment>"
}
```

This creates **one role per trust tier**, not one role — see [DECISIONS.md](DECISIONS.md) 006. It outputs the role ARNs needed in step 3.

The customer owns these roles. They can inspect, restrict, or revoke them at any time without involving us, which is the point.

**Break-glass:** the module also documents an emergency principal that can assume the tier roles independently of the platform. Do not skip this — see [SECURITY-MODEL.md](SECURITY-MODEL.md).

## 3. Register the Environment

```bash
idpctl environment create \
  --name staging \
  --provider aws \
  --account-id 123456789012 \
  --region eu-west-2 \
  --role-arn-tier1 arn:aws:iam::...:role/agentic-idp-staging-tier1 \
  --role-arn-tier2 arn:aws:iam::...:role/agentic-idp-staging-tier2 \
  --role-arn-tier3 arn:aws:iam::...:role/agentic-idp-staging-tier3 \
  --external-id <id>
```

The control plane stores role ARNs, the external ID and the trust anchor. **It stores nothing assumable on its own.**

## 4. Deploy the worker

A single binary, in the account and network where their CI and cloud live. It needs:

- Outbound reachability to the control plane (no inbound holes)
- An identity matching the `trust_anchor` from step 2
- Credentials for their CI system

## 5. Verify connectivity

```bash
idpctl environment verify staging
```

The worker attempts `sts:AssumeRole` against each tier role and reports back. This is the first real proof the integration works, and it is the Phase 0 milestone.

## 6. Create actors and teams

Teams first, since catalog entries need owners and approvals need routing.

Then agent identities. Each agent gets its own identity — **never a shared service account** — and its delegation claim is bound into the token at mint time by the authorising human:

```bash
idpctl agent create \
  --name claude-code-rhys \
  --team platform \
  --tier 2 \
  --environments staging
```

The output token is short-lived and individually revocable. The mint is audited, recording who granted it. From Phase 3 this is also a UI workflow — enrolment is deliberately not CLI-only, since the authorising human is often a team lead rather than a terminal user.

Note that **an actor cannot grant an agent more authority than it holds itself**, so whoever runs this must already hold the tier being granted. See [AGENT-MODEL.md](AGENT-MODEL.md).

## 7. Wire one pipeline

The single step with real friction. Add our composite Action to one workflow so it fetches tier-scoped credentials from the control plane rather than assuming its own role:

```yaml
- uses: <org>/agentic-idp/actions/credentials@v1
  with:
    control-plane: https://idp.internal
    # correlation is injected automatically by the trigger
```

**Be honest with prospects about this step.** It is the difference between "drop-in" and "rewire your CI", and it is the primary thing to validate before building further. If a design partner refuses here, that is the signal — not a documentation problem.

Start with one non-production workflow. Do not ask anyone to convert their estate.

## 8. First governed run

Trigger a tier-2 staging deploy as an agent. Confirm in the audit log:

- The actor is the agent, with the delegation chain resolving to the granting human
- Policy evaluated and allowed
- The credential mint is recorded with scope and TTL
- The pipeline received tier-scoped credentials, not its own role
- The run reached a terminal state with cost attributed

Then try the negative case: a tier-3 production action from the same agent should require approval, and a denied run should leave the pipeline **failing closed** rather than falling back to its own role.

---

## What we do not do

Worth stating during onboarding so expectations are set, per [SCOPE.md](SCOPE.md):

- We do not write their Terraform or their pipelines
- We do not manage Terraform state
- We do not detect infrastructure drift — we detect changes that bypassed a governed run
- We do not manage secrets

## Known friction

Tracked honestly so it can be designed away rather than defended:

| Step | Friction | Possible mitigation |
|---|---|---|
| 2 | Requires Terraform apply with elevated IAM permissions | Ship a CloudFormation one-click alternative |
| 4 | A second thing to deploy and operate | Document running it as a sidecar to existing CI runners |
| 7 | **Modifying an existing workflow** | Make the Action a single line; consider a read-only trial mode that proves value before requiring the change |
| 6 | Manual token distribution to agents | MCP-based enrolment in Phase 2 |
