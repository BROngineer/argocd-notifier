# Changelog

## [0.4.0](https://github.com/BROngineer/argocd-notifier/compare/v0.3.0...v0.4.0) (2026-09-20)


### Features

* backend api - open-api schemas and code-gen ([#18](https://github.com/BROngineer/argocd-notifier/issues/18)) ([503e728](https://github.com/BROngineer/argocd-notifier/commit/503e728692c5f91e9fb43376a7b63fb72448faf6))
* backend registry for notification backends registration ([#21](https://github.com/BROngineer/argocd-notifier/issues/21)) ([4fec545](https://github.com/BROngineer/argocd-notifier/commit/4fec545ecbc90aeb5e5a02c33560f4be5a90ede0))
* core full openapi ([#26](https://github.com/BROngineer/argocd-notifier/issues/26)) ([cc05fa4](https://github.com/BROngineer/argocd-notifier/commit/cc05fa41c74143def555451b3c3d72e0a4f32106))
* **event:** add Backend field ([#20](https://github.com/BROngineer/argocd-notifier/issues/20)) ([3799947](https://github.com/BROngineer/argocd-notifier/commit/37999477f4fa862c8d3ab49801d973843be69cea))
* multi-backend routing ([#23](https://github.com/BROngineer/argocd-notifier/issues/23)) ([062e654](https://github.com/BROngineer/argocd-notifier/commit/062e65426764ae8729eab3f9ceca0797187978af))
* remote backend adapter ([#22](https://github.com/BROngineer/argocd-notifier/issues/22)) ([a812fa5](https://github.com/BROngineer/argocd-notifier/commit/a812fa5217f8623ba6da83e4048628c0feeb01fe))
* slack standalone backend ([#24](https://github.com/BROngineer/argocd-notifier/issues/24)) ([a0f2c3f](https://github.com/BROngineer/argocd-notifier/commit/a0f2c3f9b8afdecd851913442d7942137ef86422))


### Miscellaneous

* **docs:** add remote backends design record ([082c546](https://github.com/BROngineer/argocd-notifier/commit/082c546acb55e64ee1c7f8504373833ef7822b38))
* multi-image dockerfile and chart update ([#25](https://github.com/BROngineer/argocd-notifier/issues/25)) ([cfba4ff](https://github.com/BROngineer/argocd-notifier/commit/cfba4ffbb822f638069afd69d8c0a2b564ee08ee))

## [0.3.0](https://github.com/BROngineer/argocd-notifier/compare/v0.2.1...v0.3.0) (2026-09-19)


### Features

* make notification backends support extendable ([#15](https://github.com/BROngineer/argocd-notifier/issues/15)) ([5cb6bd1](https://github.com/BROngineer/argocd-notifier/commit/5cb6bd10f069b8524cec6fe3e636342f00e910e6))


### Bug Fixes

* image tag resolution with fallback to chart's appVersion ([7cfa1c7](https://github.com/BROngineer/argocd-notifier/commit/7cfa1c78326b19b52ab97441728e15d519d81f7b))
* route notifications per-app recipient, not batch-global ([#17](https://github.com/BROngineer/argocd-notifier/issues/17)) ([d1ef5f4](https://github.com/BROngineer/argocd-notifier/commit/d1ef5f4d7ba9eff1aa7b4e5cf6c9bfe6e8041696))


### Miscellaneous

* publish image workflow ([27ed001](https://github.com/BROngineer/argocd-notifier/commit/27ed00150e2736ae2d0289b2246fc2116c01a98a))

## [0.2.1](https://github.com/BROngineer/argocd-notifier/compare/v0.2.0...v0.2.1) (2026-09-18)


### Miscellaneous

* fix versions discrepancy ([ae752a1](https://github.com/BROngineer/argocd-notifier/commit/ae752a1c339673002efc21993afe0578e77eaa9a))

## [0.2.0](https://github.com/BROngineer/argocd-notifier/compare/v0.1.0...v0.2.0) (2026-09-18)


### Features

* add Dockerfile and buildx mise tasks ([#13](https://github.com/BROngineer/argocd-notifier/issues/13)) ([fa82737](https://github.com/BROngineer/argocd-notifier/commit/fa82737fde562a6b53b70ee8c226e13e60b966a6))
* aggregator: debounce engine that batches events per group ([df2b20a](https://github.com/BROngineer/argocd-notifier/commit/df2b20ab957de950bae0ffe910bc4445162329b0))
* executable: wire all parts together and run the routines ([98cab03](https://github.com/BROngineer/argocd-notifier/commit/98cab0366ded4345f2aeae291ec5d515680a7e85))
* helm chart ([#11](https://github.com/BROngineer/argocd-notifier/issues/11)) ([3d8a337](https://github.com/BROngineer/argocd-notifier/commit/3d8a337c1e88c42767d08809a273ee7625f9ce1b))
* leader election: "failover-speed-plus-no-split-brain only, not state durability" HA ([63060b0](https://github.com/BROngineer/argocd-notifier/commit/63060b097bc46647ca2336e9aa0b94508ebb0d1a))
* pprof: add separate mux for /pprof endpoints ([7059877](https://github.com/BROngineer/argocd-notifier/commit/7059877e31f3e4c7f5d7e5cf98132df9a93cf6db))
* receiver: HTTP handler that decodes/validates/enqueues an event and acks fast (HTTP/202), plus the worker pool draining the queue ([3433d82](https://github.com/BROngineer/argocd-notifier/commit/3433d825b38cd7fe167e2c197c73a19b3ab6cf04))
* render slack: builds a Slack message (Block Kit attachments) from a session's current per-app state ([981ca54](https://github.com/BROngineer/argocd-notifier/commit/981ca54f7a7455dcf9ab41a29ae99ad68960d920))
* session store that turns batches into Slack posts/updates ([eedaf06](https://github.com/BROngineer/argocd-notifier/commit/eedaf06973fd1eb4271bca3600b437aaa8266b04))


### Miscellaneous

* add CODEOWNERS ([f315ba6](https://github.com/BROngineer/argocd-notifier/commit/f315ba6acd2b3c808df280858410f1369ebb13eb))
* add design and setup docs ([d0f5da0](https://github.com/BROngineer/argocd-notifier/commit/d0f5da0f48de9e370e2949d76bf24a253df53f3b))
* update README ([38d44f3](https://github.com/BROngineer/argocd-notifier/commit/38d44f34efc14bff6cc71536d1973ce01c7248bd))

## 0.1.0 (2026-09-17)


### Features

* **config:** add Config, load from env, validation ([3d14edc](https://github.com/BROngineer/argocd-notifier/commit/3d14edcd940eaf360d3e2d9558c0dc6f01e0eec0))
* **event:** add Event type and validation ([6797862](https://github.com/BROngineer/argocd-notifier/commit/67978625739098a5a18991cecdf9d5cbb29d4b67))
* **logging:** add structured logger constructor ([d1806de](https://github.com/BROngineer/argocd-notifier/commit/d1806deae06f5303b5883dab425637401d2ae4d8))


### Miscellaneous

* fix lint error in config tests ([77621d4](https://github.com/BROngineer/argocd-notifier/commit/77621d4fb507f7e0b8023c1291cd5af5f77eb473))
* go mod init ([292fea1](https://github.com/BROngineer/argocd-notifier/commit/292fea1c7bcf2271480f1611cc7ca128bb2329e2))
* golangci-lint config ([d3ba7eb](https://github.com/BROngineer/argocd-notifier/commit/d3ba7eb7f39c82cd1eb9fe8469a68e3f4def0941))
* mise configuration ([116f4be](https://github.com/BROngineer/argocd-notifier/commit/116f4beeaf7e0aeef826d9cbfcac7e0dfd529dd9))
* update readme ([b42b8f8](https://github.com/BROngineer/argocd-notifier/commit/b42b8f8f2f228a448f5924023716f44498bf14a0))
* workflows and configurations update ([#1](https://github.com/BROngineer/argocd-notifier/issues/1)) ([e3133c6](https://github.com/BROngineer/argocd-notifier/commit/e3133c6c45716a3099f0ee0c03b1dd7732e107a7))
