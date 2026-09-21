# agentic-idp

An Internal Developer Platform where **both human developers and AI agents** build, test and deploy software through a governed self-service catalog.

Agent identity, trust tiers, policy, approval, audit and cost attribution are first-class concepts — enforced identically whether the actor is a person or a machine.

> **Status: pre-implementation.** No code yet. These documents are the design, written before the first commit deliberately.

## The scope boundary

> **We govern and record actions. We do not author infrastructure, and we do not own desired state.**

When a feature request arrives, that sentence is the test. See [SCOPE.md](docs/SCOPE.md).

## Documentation

| Document | What it covers |
|---|---|
| [PLAN.md](docs/PLAN.md) | Problem, competitive position, business model, licensing |
| [SCOPE.md](docs/SCOPE.md) | What this is and is not; responsibility split; non-goals |
| [ARCHITECTURE.md](docs/ARCHITECTURE.md) | Components, data model, key flows, CI adapter design |
| [AGENT-MODEL.md](docs/AGENT-MODEL.md) | How agents are enrolled, authenticated, scoped and used |
| [SECURITY-MODEL.md](docs/SECURITY-MODEL.md) | Trust boundaries, credential handling, break-glass |
| [V1-ROADMAP.md](docs/V1-ROADMAP.md) | Phases, milestones, deferred work, open decisions |
| [DECISIONS.md](docs/DECISIONS.md) | Numbered decision log with rationale |
| [ONBOARDING.md](docs/ONBOARDING.md) | What a customer has to do to adopt this |

## Deployment model

Customers **self-host**. The control plane runs in their infrastructure; a worker runs in their cloud account and holds credentials. We never hold customer cloud credentials. A hosted offering may follow later — same binary, not a second product.

## Licence

BSL 1.1, converting to Apache 2.0 four years after each release. Non-production use is free. See [LICENSE](LICENSE).
