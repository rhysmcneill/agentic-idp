# CI integration: the OIDC callback

How a triggered pipeline gets tier-scoped cloud credentials, and how to wire a CI job to fetch them. Companion to [ARCHITECTURE.md](ARCHITECTURE.md)'s "CI OIDC callback" section, which has the full sequence diagram — this doc is the operational/reference side: what to configure, what the contract is, and what's advisory versus what we maintain.

## What we own vs. what's the customer's

Two responsibilities, and nothing more:

1. **Starting the run, once approved** — the GitHub Actions adapter's `Trigger`.
2. **Answering "who are you, and what can you have" when a job asks** — the OIDC callback: verify the token, match it to the run it authorises, re-check policy, mint tier-scoped credentials, audit the mint.

We do not own or maintain any CI-side glue beyond `idpctl ci auth` itself — no published composite GitHub Action, no required-workflow injection. `idpctl ci auth` is the *recommended* path, not the only one: the raw callback contract below is fully documented so a customer can call it directly with any HTTP client if `idpctl` doesn't fit their pipeline.

## Setup, once per repo

1. `idpctl pipeline register` the repo/workflow (see [ONBOARDING.md](ONBOARDING.md) step 8) — **must happen before the first CI run**, since discovery below joins through the registered pipeline.
2. Add to the workflow: `permissions: id-token: write`, and a step calling `idpctl ci auth`.

That's the entire customer-facing surface. Everything past this point is what happens underneath it.

## The discovery step: no `--callback-url` to configure per job

The worker's callback needs an externally-reachable address, and that address becomes the OIDC `aud` value the job requests its token for — this is standard OIDC convention (AWS uses `sts.amazonaws.com`, GCP Workload Identity Federation uses the identity pool provider's resource name, Kubernetes service-account tokens name their intended audience explicitly), not something particular to this project. A process can never introspect its own externally-reachable URL — that depends on ingress/load-balancer/DNS configuration entirely outside its own visibility, the same reason GitLab (`external_url`), Keycloak (`KC_HOSTNAME`) and most other self-hosted software require this told to them rather than inferred.

Rather than configure that same address twice (once on the worker, once in every CI job), the worker registers it once and `idpctl ci auth` discovers it:

- The worker announces its own `IDP_CI_CALLBACK_URL` once at startup via `PUT /v1/worker/ci-callback-url` (worker-authenticated).
- `idpctl ci auth` discovers it via `GET /v1/ci/callback-url?provider=github_actions&repo=<owner>/<name>` (unauthenticated — the value isn't sensitive, the OIDC signature check is the real security boundary).

The `provider`+`repo` query parameters aren't optional decoration. Discovery is scoped through the same `pipelines` join `ResolveExternalRef` uses for correlation, so a worker credential can only ever answer discovery for a repo whose registered pipeline lives in an environment that worker is actually granted. An earlier, unscoped version of this endpoint (answering with "whichever worker registered most recently, tenant-wide") was a real cross-environment privilege boundary — a compromised low-trust worker credential could redirect a completely unrelated production repo's credential-fetch traffic to itself. Caught by security review before release; not a hypothetical.

## The callback contract

`POST <discovered callback URL>`

Request:
```json
{ "provider": "github_actions", "token": "<the CI platform's own OIDC ID token>" }
```

Response (200):
```json
{
  "access_key_id": "...",
  "secret_access_key": "...",
  "session_token": "...",
  "expiration": "2026-01-01T12:34:56Z"
}
```

Non-200 means no credentials were minted — the caller gets nothing usable either way, matching the fail-closed rule everywhere else credential minting happens in this system. Common causes: token failed verification (bad signature, wrong audience, expired), no matching run found (pipeline not registered, or the run isn't currently `executing`), or the run's tier no longer permits the pipeline (policy re-checked at this exact point, since a pipeline's `mutating` flag can change in the gap between dispatch and a job actually starting).

## `idpctl ci auth`

```
idpctl ci auth [--server <control-plane-url>] [--format json|env|credential-process]
```

- **Detects the CI platform** it's running under (checks `GITHUB_ACTIONS`; only GitHub Actions is supported today) and the repo (`GITHUB_REPOSITORY`, set automatically by GitHub).
- **Discovers** the callback URL from the control plane (see above).
- **Fetches the platform's own OIDC token** — for GitHub Actions, via `ACTIONS_ID_TOKEN_REQUEST_URL`/`ACTIONS_ID_TOKEN_REQUEST_TOKEN` (present only when the job declares `permissions: id-token: write`), requesting it with `audience` set to the discovered callback URL.
- **Calls the callback**, prints credentials in the requested `--format`.

Formats:
- `json` (default) — the raw response shape above, indented.
- `env` — `export AWS_ACCESS_KEY_ID=...` lines, each value properly shell-quoted (safe to `eval` directly; this matters because the whole point of this format is to be `eval`'d).
- `credential-process` — the exact JSON shape (`Version`/`AccessKeyId`/`SecretAccessKey`/`SessionToken`/`Expiration`) the AWS SDK expects from a [`credential_process`](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-envvars.html) source, so `idpctl ci auth --format=credential-process` can be that source directly in an AWS profile:

```ini
[profile idp-governed]
credential_process = idpctl ci auth --format=credential-process
```

This is the recommended integration style for anything beyond a single short-lived step — the AWS SDK/CLI calls the process itself, on demand, whenever cached credentials are near expiry, so a deploy that runs longer than the credential TTL just keeps working instead of failing partway through with stale keys.

## Worked example: GitHub Actions

```yaml
on:
  workflow_dispatch:

permissions:
  id-token: write
  contents: read

jobs:
  deploy:
    runs-on: ubuntu-latest
    env:
      AWS_PROFILE: idp-governed
      AWS_CONFIG_FILE: ${{ github.workspace }}/.aws/idp-config
    steps:
      - name: Configure governed AWS credentials
        run: |
          mkdir -p "$(dirname "$AWS_CONFIG_FILE")"
          cat > "$AWS_CONFIG_FILE" <<EOF
          [profile idp-governed]
          credential_process = idpctl ci auth --format=credential-process
          EOF

      - name: Deploy
        run: ./deploy.sh   # any aws-cli/terraform/SDK call just works
```

Nothing in the `Deploy` step is credential-shaped — no secret ever appears in a variable a workflow author could accidentally echo into a log.

## Extending to another CI provider

Not needed for v1 (GitHub Actions is the only adapter — GitLab CI/CD, Bitbucket Pipelines, Jenkins and Atlantis are deferred, see [V1-ROADMAP.md](V1-ROADMAP.md)), but the design doesn't need to change when one is added:

- `worker/internal/ciauth` is provider-agnostic internally: a `Verifier` interface (`Provider() ci.Provider`, `Verify(ctx, rawToken) (Claims, error)`) plus a `Registry`, the same shape as `pkg/ci.Registry`. Adding GitLab means writing a `gitlabci.Verifier` and registering it — the callback handler, the matching logic, and the mint/audit path don't change.
- GitLab CI/CD and an eventual HCP Terraform/Atlantis-style adapter both issue OIDC ID tokens the same way GitHub Actions does, so the same `coreos/go-oidc`-based verification approach applies directly.
- **Not the same problem, and not solved by this mechanism:** GCP Workload Identity Federation and AWS IAM OIDC providers run in the *opposite* direction — they're how a cloud provider verifies a token *presented to it* by an external workload. If this worker ever authenticates outbound to GCP using workload identity federation, that's a token-exchange client call (analogous to `worker/internal/broker/aws`'s `sts:AssumeRole`), not another `ciauth.Verifier`.
