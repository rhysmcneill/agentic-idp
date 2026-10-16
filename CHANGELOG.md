# Changelog

## 0.1.0 (2026-10-16)


### Features

* add optional telemetry and cost runs ([#28](https://github.com/rhysmcneill/agentic-idp/issues/28)) ([28c2d67](https://github.com/rhysmcneill/agentic-idp/commit/28c2d6758f1027087f6d42645cd5105786205cc3))
* **ci:** add GitHub Actions adapter and CI OIDC credential-broker callback ([#24](https://github.com/rhysmcneill/agentic-idp/issues/24)) ([2df81fc](https://github.com/rhysmcneill/agentic-idp/commit/2df81fc110b99f1a58714e8ab65e005b24b215aa))
* **controlplane:** add Postgres storage for tenant/team/actor/enviro… ([1b99249](https://github.com/rhysmcneill/agentic-idp/commit/1b99249773ee7850b25933de5e49de2a67ae6caf))
* **controlplane:** add Postgres storage for tenant/team/actor/environment ([1b99249](https://github.com/rhysmcneill/agentic-idp/commit/1b99249773ee7850b25933de5e49de2a67ae6caf))
* **controlplane:** add Postgres storage for tenant/team/actor/environment/audit ([d424998](https://github.com/rhysmcneill/agentic-idp/commit/d4249985c136808a8e40004887f9febf796882c2))
* **core:** add pipelines, river queues and tier-based governance via policies ([#20](https://github.com/rhysmcneill/agentic-idp/issues/20)) ([dbb010c](https://github.com/rhysmcneill/agentic-idp/commit/dbb010c9b4aea3f91702688ee547a8e1d4e28eb5))
* **delivery:** add docker-compose local dev stack, Floci-based AWS emulation testing, and full e2e CI ([#2](https://github.com/rhysmcneill/agentic-idp/issues/2)) ([#19](https://github.com/rhysmcneill/agentic-idp/issues/19)) ([66f1981](https://github.com/rhysmcneill/agentic-idp/commit/66f198148bf5b45ec121daf8683507a4cc5895fb))
* Phase 0 core — trust model, enrolment, and worker skeleton ([#14](https://github.com/rhysmcneill/agentic-idp/issues/14)) ([f9dfc7e](https://github.com/rhysmcneill/agentic-idp/commit/f9dfc7e15d298f8dd65a8459b7395e81959797a7)), closes [#1](https://github.com/rhysmcneill/agentic-idp/issues/1)
* **sessions:** implement jti claims to issued tokens and add token revoke checks ([#13](https://github.com/rhysmcneill/agentic-idp/issues/13)) ([2860b5f](https://github.com/rhysmcneill/agentic-idp/commit/2860b5fc4fa065e01aa2049c64b88f9cdcb8e0ce))
* **worker:** self-registration, multi-account environment grants, and hub-and-spoke docs ([#16](https://github.com/rhysmcneill/agentic-idp/issues/16)) ([#18](https://github.com/rhysmcneill/agentic-idp/issues/18)) ([5445c61](https://github.com/rhysmcneill/agentic-idp/commit/5445c61d3d43de4f8848dae329596a95cd960853))


### Bug Fixes

* **tests:** flakey race tests ([#25](https://github.com/rhysmcneill/agentic-idp/issues/25)) ([83c2c33](https://github.com/rhysmcneill/agentic-idp/commit/83c2c33d0e59150c431b74644e5b010f89abcccf))
