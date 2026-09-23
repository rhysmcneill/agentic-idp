# Repository Structure

A monorepo containing every component: control plane, MCP server, worker, CLI, frontend, deployment assets and docs. **This document is canonical** — if the tree on disk and this file disagree, one of them is a bug.

A single Go module at the root. Splitting into multiple modules buys little for a solo codebase and costs version skew on every cross-cutting change; revisit only if an external consumer needs to import a subset.

## Tree

```
agentic-idp/
  pkg/                          # public packages; extraction candidates for the Apache library
    ci/                         # generic CI adapter interface (GitHub Actions is the only impl)
    identity/                   # actor model, token-bound delegation claims
    ciauth/                     # CI OIDC token verification (JWKS, claim checking)
    cloud/                      # generic cloud credential broker interface (AWS is the only impl)

  controlplane/                 # API and control plane — holds NO cloud credentials
    cmd/server/
    internal/
      tenant/                   # tenant_id exists from the first migration
      team/                     # owners for catalog entries, routing for approvals
      actor/                    # humans and agents as typed principals
      environment/              # tier role ARNs, external IDs, trust anchor
      workercred/               # the worker's own hashed bearer credential — not an identity.Actor
      verification/             # environment_verifications: the connectivity-check job queue
      policy/                   # typed trust tiers (OPA from Phase 2)
      run/                      # state machine, job queue, approvals, run authorisation
      audit/                    # append-only
      cost/                     # per-run, rolling up per actor
      catalog/                  # Phase 2
      api/                      # HTTP handlers, OpenAPI spec
    migrations/

  mcp/                          # MCP server — SEPARATE service/pod/container
    cmd/mcp-server/
    internal/tools/             # tool definitions, tier-filtered exposure

  worker/                       # customer-deployed; the ONLY component holding cloud credentials
    cmd/worker/
    internal/
      service/                  # wires config, broker and poller together; cmd/worker/main.go's thin Run
      poller/                   # the main loop: poll for a job, execute it, report back
      controlplane/             # the worker's own HTTP client, authenticating with its worker credential
      config/                   # env-var config, incl. reading the worker token from a file or env var
      broker/
        aws/                    # the only caller of sts:AssumeRole; mints tier-scoped credentials
        # gcp/ azure/ — deferred, see docs/DECISIONS.md 017
      ciauth/                   # receives pipeline OIDC callbacks, verifies, mints via broker
      executors/
        pipeline/
          githubactions/        # the only adapter in v1

  cli/
    cmd/idpctl/
    internal/

  frontend/                     # React + TypeScript, Vite (Phase 3)
    src/

  actions/                      # composite GitHub Actions customers add to their workflows
    credentials/                # fetches tier-scoped credentials via OIDC callback

  deploy/
    aws/                        # tier-role Terraform module for customer onboarding
    helm/                       # control plane, MCP server and worker as separate deployments
    docker-compose.yml          # local development and evaluation

  .github/workflows/            # our own CI
  examples/                     # sample catalog entities, agent integration, environment setup
  docs/
  LICENSE                       # BSL 1.1
```

## Deployables

Four binaries and one web app, deployed independently:

| Component | Where it runs | Holds cloud credentials | Network |
|---|---|---|---|
| `controlplane` | Customer infrastructure | **No** | Serves the API |
| `mcp-server` | **Separate service/pod/container** | No | Client of the control plane API |
| `worker` | Customer's cloud account | **Yes**, in memory only | Outbound to control plane; inbound only from the customer's own CI |
| `idpctl` | Developer machines and agents | No | Client of the control plane API |
| `frontend` | Static assets | No | Client of the control plane API |

### Why the MCP server is a separate service

It is deployed as its own pod/container, not embedded in the control plane, because:

- **It scales independently.** Agent traffic has a different shape from UI and CLI traffic.
- **It can sit closer to agents**, including in a different network zone from the control plane, or as several instances serving different environments.
- **It is unprivileged, and keeping it separate keeps it that way.** A separate process with no database access cannot quietly acquire authority it should not have.

**It must not become a confused deputy.** The MCP server does not hold a privileged credential and does not authenticate to the control plane on its own behalf. It **forwards the agent's token**; the control plane verifies that token and its bound delegation claim. If the MCP server ever substituted its own identity for the agent's, the delegation chain would terminate at the MCP server and the entire audit story would fail. It is a thin, unprivileged translator between MCP tool calls and the control plane API — nothing more.

## Where credentials live

This layout encodes the security model, and the placement is deliberate rather than incidental.

- `worker/internal/broker/aws/` is the **only** code that calls `sts:AssumeRole`. Nothing outside the worker mints cloud credentials. It implements the provider-agnostic `pkg/cloud.Broker` interface (see [ARCHITECTURE.md](ARCHITECTURE.md)) so GCP/Azure brokers are additive later rather than a rewrite — but only the AWS implementation ships in v1.
- `worker/internal/ciauth/` receives OIDC callbacks **from the customer's pipelines**, not from the internet. The worker sits in their account alongside their CI, so this is an internal boundary.
- `pkg/ciauth/` holds only the stateless parts — JWKS fetching, signature verification, claim checking — so they are shared and independently testable. It mints nothing.
- `controlplane/` answers *"is correlation X authorised, and at what tier?"* It never sees a cloud credential.

### Why the OIDC callback targets the worker, not the control plane

An earlier draft put `ciauth` in the control plane. That contradicted the security model: verifying a pipeline's OIDC token and then handing it credentials requires calling STS, which would have made the control plane a credential holder.

Routing the callback to the worker resolves it. The worker verifies the token, asks the control plane over its existing outbound connection whether the correlation maps to an approved run, and only then mints. The control plane stays credential-free — which matters most for a future hosted offering, where it would sit outside the customer's boundary entirely.

The worker accepting inbound connections from the customer's own CI is a materially different risk from the control plane doing so, and "outbound-only" still holds for the link that crosses a trust boundary.

## Conventions

- `pkg/` is public API and a candidate for extraction into the Apache 2.0 library. Anything placed there should make sense to a stranger importing it alone.
- `internal/` is private to its parent component and may be refactored freely.
- Components communicate over the control plane's REST API, never by importing each other's `internal/`.
- Empty directories carry a `.gitkeep` so the documented structure is visible before the code exists.
