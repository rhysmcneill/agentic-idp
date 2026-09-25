# Worker AWS Authentication

This doc is entirely about the **worker's** relationship to AWS across N customer accounts. The control plane never appears in it: it only ever stores config — role ARNs, external ID, trust-anchor string — never anything assumable on its own (see [SECURITY-MODEL.md](SECURITY-MODEL.md)'s "Credential handling"). If a question here starts with "how does the control plane authenticate to AWS," the answer is that it doesn't, and never will.

## The worker's own base identity

The worker authenticates to AWS as *itself* first, using whatever identity its deployment substrate gives it — resolved by the AWS SDK's default credential chain (`awsconfig.LoadDefaultConfig`, `worker/internal/broker/aws/aws.go:71`, called from `NewDefault`). In practice this is one of:

- An EKS IRSA role or EKS Pod Identity, if the worker runs as a Kubernetes pod
- An EC2 instance profile, if it runs on a bare EC2 host
- An ECS task role, if it runs as an ECS task

This identity lives in whatever "hub" account or cluster the worker is deployed into — a shared-services account is the common choice, not any one customer workload account. There is exactly one such identity per worker process; it is never customer-account-specific.

## Reaching N target accounts from one identity

`sts:AssumeRole` is a cross-account primitive: the caller and the target role never need to share an account. Each target account's per-tier role ([Decision 006](DECISIONS.md) — one role per trust tier, not session-policy scoping) carries a trust policy naming the worker's base identity as principal, gated by an `sts:ExternalId` condition unique to that `Environment` — the standard confused-deputy mitigation.

A target role's trust policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": { "AWS": "arn:aws:iam::<hub-account-id>:role/agentic-idp-worker" },
    "Action": "sts:AssumeRole",
    "Condition": {
      "StringEquals": { "sts:ExternalId": "<per-environment-external-id>" }
    }
  }]
}
```

This matches exactly what the broker sends (`assumeRole`, `worker/internal/broker/aws/aws.go:156`): `RoleArn` (the target tier role), `RoleSessionName` (fixed to `assumeRoleSessionName`, `"agentic-idp-worker"` — sessions are correlated via our own audit log, not the customer's CloudTrail), `ExternalId`, and `DurationSeconds` (15 minutes, `defaultCredentialTTL`). Nothing about this call is account-specific to the worker's own identity — only the `RoleArn`/`ExternalId` pair, which comes fresh from each `Environment`'s stored config on every call.

## Worked example: one worker, three accounts

- Worker runs as an EKS pod in a `shared-services` account, under IRSA role `agentic-idp-worker`.
- Three customer workload accounts (`staging`, `prod-a`, `prod-b`) each apply the Terraform module described in [ONBOARDING.md](ONBOARDING.md) step 2, once per account. Each creates three tier roles (`ReadOnly`/`HumanInTheLoop`/`Autonomous`), each trusting `arn:aws:iam::<shared-services-account>:role/agentic-idp-worker` with its own external ID.
- Three `Environment`s are registered against the control plane (`idpctl environment create`, once per account), each storing that account's role ARNs, external ID and trust anchor.
- The same worker credential is granted all three environments and serves all three from the one running process.

`deploy/aws` is currently a `.gitkeep` stub — no Terraform module exists yet. This doc is the spec that module implements, not a description of code that exists today.

## Scaling: adding account #1001

Onboarding a 1,001st account never touches the worker:

1. Customer applies the same Terraform module in the new account (their side, unchanged).
2. `idpctl environment create` registers the new `Environment` against the control plane.
3. The new environment is granted to the existing worker credential (the grant endpoint from [Decision 022](DECISIONS.md)'s bootstrap work) — no worker redeploy, no code change.

## Non-goals

- **GCP/Azure** — deferred; see [Decision 017](DECISIONS.md). Everything above is AWS-specific by design.
- **CI-OIDC-callback verification** — a different mechanism entirely (a pipeline proving its own identity to the worker), covered by [SECURITY-MODEL.md](SECURITY-MODEL.md)'s "The CI boundary" section and unbuilt Phase 1 work (`internal/ciauth`). Not duplicated here.
- **Break-glass** — already covered in [SECURITY-MODEL.md](SECURITY-MODEL.md); not repeated here.
