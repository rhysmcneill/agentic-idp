# Delivery: CI/CD, Containers, Releases

This is a track that runs alongside the phases in [V1-ROADMAP.md](V1-ROADMAP.md), not a phase of its own. It didn't have a home in the original roadmap because it isn't a product feature — but several of the roadmap's own milestones are not actually true without it, so it needs the same phase-by-phase discipline as everything else.

## Why this isn't "Phase 5"

Two of the roadmap's existing milestones silently assume delivery infrastructure exists:

- Phase 0's milestone is *"a **customer-run worker** executes a job by assuming a real tier role."* That is not true of a Go binary that only exists as source in this repo — it requires something a customer can pull and run.
- Phase 1's milestone is *"demoable to design partners."* A design partner cannot deploy something that isn't published anywhere.

So delivery capability has to land **at or before** the roadmap phase whose milestone depends on it, not after.

## What's in scope

| Capability | What it is |
|---|---|
| Makefile | Single command interface — lint, format, build, test, docker, publish — that CI calls into rather than duplicating |
| Pre-commit hooks | Local `pre-commit` framework, same shape as [ssmctl](https://github.com/rhysmcneill/ssmctl)'s config, mirroring Makefile targets |
| CI | Build, vet, format check, lint, test — on every PR |
| Dockerfiles | One per deployable service, written when that service's entrypoint lands, not batched upfront: `controlplane`/`worker` in Phase 0, `mcp` in Phase 2, `frontend` in Phase 3 |
| Image publishing | Tagged builds pushed to a registry on merge and release |
| Versioning | `semantic-release`, driven by conventional commits |
| Dependency updates | Dependabot (or Renovate) |
| Coverage | Codecov |
| Helm / Compose | Deployment manifests, tracking the same versions as the images |

## Makefile: single entry point for delivery commands

A repo-root `Makefile` is the interface for every delivery command below — CI workflows invoke `make <target>`, contributors run the identical command locally, and there is exactly one place that knows how to lint, format, build, test, containerise and publish this repo.

Based directly on [ssmctl's Makefile](https://github.com/rhysmcneill/ssmctl/blob/main/Makefile), which already encodes this project's Go quality bar and developer setup. Reused as-is:

- `fmt` (`gofmt -w -s .` + `goimports -w .`), `fmt-check` (the CI-safe non-mutating check), `vet`, `lint` (`golangci-lint run`), `test`, `test-cover` (`-coverprofile` + `go tool cover -func`)
- `setup` — one-time developer bootstrap: installs `golangci-lint`, `goimports`, `gosec`, and runs `pre-commit install` (both the standard and `commit-msg` hook types) if `pre-commit` is present
- `pre-commit-hooks-update` — `pre-commit clean && pre-commit install-hooks`
- `ci` — the composite local target mirroring what CI runs on a PR (ssmctl's is `vet test build e2e`; this repo's runs `vet fmt-check lint test build`)
- `bench` / `bench-compare` — kept for any package where benchmarks matter (e.g. `pkg/ci` adapter dispatch), using `benchstat` against a committed `baseline.txt`

What's added, specific to this being a multi-service monorepo rather than a single CLI binary:

- `build-<service>` per deployable (`controlplane`, `worker`, `mcp`; `frontend` from Phase 3) in place of ssmctl's single `build`/`build-all` (which cross-compiles one binary for multiple OS/arch — not needed here, since every deployable ships as a container image, not a downloaded binary)
- `docker-build-<service>` and `docker-build-all`
- `publish` — tagged image push, gated to CI's `main`/tag-triggered job, never runnable with a contributor's local publish credentials
- `helm-lint` / `helm-template` once the Helm chart lands in Phase 1

Targets land incrementally as the capability they wrap exists, but the shape above is fixed from the start. Adding a capability (a new service, a new gate) means adding a target, not inventing a new place to encode the command.

## Placement by phase

### Phase 0 — the floor, from the first commit

- **Makefile**: `make lint`/`fmt`/`fmt-check`/`vet`/`test`/`build` land alongside the first Go code, with `make ci` as the composite target CI itself invokes.
- **Pre-commit hooks**: `pre-commit`, configured the same shape as [ssmctl's](https://github.com/rhysmcneill/ssmctl/blob/main/.pre-commit-config.yaml) — general hygiene hooks (trailing whitespace, EOF fixer, large-file check, merge-conflict markers, no direct commits to `main`), Go-specific checks as **local** hooks (`go vet`, `golangci-lint`, `gosec` — local because the upstream Go pre-commit plugins build against their own toolchain, which can mismatch this module's `go` directive), `detect-secrets` with a committed baseline (ties directly to the secrets-handling invariant in [CLAUDE.md](../CLAUDE.md)), and `commitlint` enforcing the conventional-commit format at commit-msg stage. Mirrors Makefile targets so the local check and the CI check are the same command.
- **CI**: `go build ./...`, `go vet ./...`, `gofmt -l` (fail on output), `go test ./...`, then `golangci-lint` once configured, all invoked via the Makefile. This is [CLAUDE.md](../CLAUDE.md)'s existing CI/CD bar, restated here as a delivery milestone rather than a background assumption.
- **Dependabot**: enabled from the first `go.mod`. `pkg/ci` and the credential-handling packages are the ones that matter most here — see [CLAUDE.md](../CLAUDE.md) Security invariants.
- **Codecov**: wired alongside CI. Cheap, and the point of doing it now is establishing the coverage number before code accumulates that quietly lowers it — a baseline set later is not a baseline, it's a negotiation.
- **Conventional commits**: adopted now even though nothing consumes them yet, specifically so that turning on `semantic-release` in Phase 1 is a config change, not a rewrite of commit history or a "starting now" carve-out.
- **Dockerfiles**: `controlplane` and `worker` get one each, plus `docker-compose.yml` for local dev. This is not optional polish — the Phase 0 milestone requires a *customer-run* worker, and "customer-run" means containerised, not `go run` from a cloned repo. Sequencing: each service's Dockerfile is written once that service has a real `cmd/` entrypoint to build, not upfront against an empty skeleton — `controlplane`'s lands with its `cmd/server` work, `worker`'s with its `cmd/worker` work, `docker-compose.yml` once both exist to wire together. The Makefile's `docker-build-*` targets and Phase 0 tooling (CI, pre-commit, Dependabot, Codecov, conventional commits) still land first, since those apply from the first line of Go code regardless of which service it's in.

### Phase 1 — publishing and versioning go live

- **Image publishing**: `controlplane` and `worker` images built and pushed to a registry (GHCR is the default choice — free for public/private repos, no separate account to manage, integrates with GitHub Actions directly) on merge to `main` and on tagged release.
- **`semantic-release`**: turned on here, not before, because Phase 1 is the first point there's a real consumer (a design partner) who needs a real version to pull. Running it from Phase 0 would just generate version noise with no audience.
- **Helm chart**: introduced alongside the images, since Phase 1 is also the first phase where a design partner might deploy the platform rather than just hear about it.

### Phase 2 — scales, doesn't change shape

The MCP server becomes a fourth image in the same pipeline, at the same version. If Phase 1's pipeline was built to publish "N services," this is zero new design — see the versioning decision below for why that matters.

### Phase 3 — frontend joins

The frontend is built, versioned and published at the same release as the backend. `impeccable audit` findings (accessibility, performance, responsive behaviour) become a CI gate here, not an aspiration — see [CLAUDE.md](../CLAUDE.md) and [V1-ROADMAP.md](V1-ROADMAP.md) 3b on why accessibility is a sales requirement, not polish.

## Versioning: one number for the whole monorepo

**Decision: a single semver for the entire release, applied identically to every image tag** (`controlplane:1.4.0`, `worker:1.4.0`, `mcp:1.4.0`), not independent per-service versions.

This is not the default most monorepo tooling nudges you toward — independent per-package versioning is common and `semantic-release` supports it. It's rejected here for a reason specific to this product:

[`docs/PLAN.md`](PLAN.md) already names **version sprawl** as an accepted cost of self-hosting — customers run whatever they installed, and several versions need support simultaneously. Independent per-service versions would compound that into a compatibility matrix: *does worker 2.1 talk to control-plane 1.9?* That question should never need asking. A self-hosted customer, and a support engineer helping them, should be able to say "we're running 1.4.0" as a complete description of what's deployed.

The cost is real and accepted: a docs-only change to the frontend bumps the same version number as a broker security fix. That's a fine trade for legibility in a product whose core sale is "you can tell exactly what's running and who authorised it" — the versioning scheme should not undermine the same property the product exists to provide elsewhere.

## Registry and secrets

- **GHCR** (`ghcr.io`) is the default: free, no separate signup, authenticates via the same GitHub Actions token already needed for CI.
- Publishing credentials live in GitHub Actions secrets, never in the repo, never in a Dockerfile — this is the same rule as [CLAUDE.md](../CLAUDE.md)'s secret-handling standard, applied to our own pipeline rather than the product.
- Image publishing is a separate CI job from build/test, gated on `main`/tags only, so a PR from an external contributor (once the CLA is in place) never has publish credentials in scope.

## Dockerfile conventions

- Multi-stage builds: a Go build stage, a minimal runtime stage (`distroless` or equivalent) — no compiler, no shell, no package manager in the shipped image. This isn't generic best practice for its own sake here: the `worker` image holds cloud credentials in memory at runtime, so its attack surface should be as close to zero as the build allows.
- Non-root user in every runtime image.
- No secrets baked into any image layer, ever — this is the same invariant as the control plane never holding credentials, applied to the image build itself rather than the running process.

## Open questions

**Where does `idpctl` (the CLI) get distributed?** Not a container concern — it needs its own path (GitHub Releases with prebuilt binaries, a Homebrew tap, or both). Not yet placed in a phase; revisit once Phase 1 nears completion, since design partners running the onboarding sequence in [ONBOARDING.md](ONBOARDING.md) will need it.

**How does the API get an OpenAPI spec, given `CLAUDE.md`'s "REST, OpenAPI-documented" rule?** Deliberately deferred rather than picked under time pressure with three handlers to look at. `swaggo/swag` was tried and reverted — it only generates Swagger 2.0 (its own maintainers have said OpenAPI 3 won't be supported), and its comment annotations are unenforced free text with the same drift risk as a hand-written spec. `oapi-codegen` (spec-first, generates a `ServerInterface` the compiler enforces) and `huma` (code-first, wraps stdlib `net/http`, spec derives automatically from struct tags — no annotations or hand-written YAML at all) are the two credible options; `huma` fits this project's "no manual spec authoring, ever" requirement best but means moving handlers into its input/output-struct calling convention, a real restructure. Revisit once the API surface is bigger than three endpoints and the actual pain of not having one is felt, rather than speculating now.
