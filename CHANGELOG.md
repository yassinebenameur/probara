# [1.0.0-alpha.35](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.34...v1.0.0-alpha.35) (2026-03-11)


### Features

* **local:** improve status pages and local workflows ([3f4e553](https://github.com/yassinebenameur/probara/commit/3f4e55345e99027716f6d3f6cae6628102290a17))

# [1.0.0-alpha.34](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.33...v1.0.0-alpha.34) (2026-03-10)


### Bug Fixes

* **dashboard:** wrap overflowing panel content ([cf55e6e](https://github.com/yassinebenameur/probara/commit/cf55e6e03345376916b525fbe9707f96212c642a))

# [1.0.0-alpha.33](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.32...v1.0.0-alpha.33) (2026-03-10)


### Features

* **status-page:** add theme settings and dedicated renderer ([1c1bd23](https://github.com/yassinebenameur/probara/commit/1c1bd23bf035c7823d2f0d4cb9d5d8fe530a93cc))

# [1.0.0-alpha.32](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.31...v1.0.0-alpha.32) (2026-03-09)


### Bug Fixes

* **worker:** recover consumer loop after nats heartbeat loss ([e4ba74b](https://github.com/yassinebenameur/probara/commit/e4ba74b6adb2adbf8fdb1383b5c4dc3996379d7c))

# [1.0.0-alpha.31](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.30...v1.0.0-alpha.31) (2026-03-09)


### Features

* add portable monitor export and reimport support ([430dc38](https://github.com/yassinebenameur/probara/commit/430dc38e774659f3ee24f826eb4757777d4a37e8))

# [1.0.0-alpha.30](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.29...v1.0.0-alpha.30) (2026-03-07)


### Bug Fixes

* **queue:** retain check jobs as work queue ([d661b9d](https://github.com/yassinebenameur/probara/commit/d661b9d52f0e1742b0e54bb274614ecde54e29b4))

# [1.0.0-alpha.29](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.28...v1.0.0-alpha.29) (2026-03-07)


### Bug Fixes

* **api:** use core nats for alert SSE fanout ([f933e6a](https://github.com/yassinebenameur/probara/commit/f933e6a9c6b0c26f3ba54b675fe4ffdae809d566))

# [1.0.0-alpha.28](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.27...v1.0.0-alpha.28) (2026-03-06)


### Bug Fixes

* **api:** fan out alert events to all SSE pods ([6c5e819](https://github.com/yassinebenameur/probara/commit/6c5e81978349fe2624d46316b46596b83a61d685))

# [1.0.0-alpha.27](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.26...v1.0.0-alpha.27) (2026-03-06)


### Bug Fixes

* **web:** replay active alert toasts on connect ([ac31044](https://github.com/yassinebenameur/probara/commit/ac3104478eda1d8bb991b25ec930ac9e946f7e9f))

# [1.0.0-alpha.26](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.25...v1.0.0-alpha.26) (2026-03-06)


### Bug Fixes

* **api:** start alert SSE subscriber ([d213308](https://github.com/yassinebenameur/probara/commit/d213308352f1840f2b83c3ddb1e1b0224c1c693f))

# [1.0.0-alpha.25](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.24...v1.0.0-alpha.25) (2026-03-06)


### Features

* **web:** add global alert SSE toasts ([e52eb93](https://github.com/yassinebenameur/probara/commit/e52eb937572f3c3b1aa261df0cc0179f6e69b0f6))

# [1.0.0-alpha.24](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.23...v1.0.0-alpha.24) (2026-03-06)


### Bug Fixes

* **scheduler:** log rollup backfill progress ([745f10c](https://github.com/yassinebenameur/probara/commit/745f10c1478f80442f10de6efee970e330cc12ac))


### Features

* **dashboard:** add tag-scoped stats filtering ([8bf1cfb](https://github.com/yassinebenameur/probara/commit/8bf1cfb171c7fb8acff9d64ace9d42d1343e4e66))

# [1.0.0-alpha.23](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.22...v1.0.0-alpha.23) (2026-03-06)


### Bug Fixes

* **frontend:** show gaps for incomplete dashboard rollups ([8dab682](https://github.com/yassinebenameur/probara/commit/8dab6829af4f2e77326dc0c6ddce0c558b4091bc))

# [1.0.0-alpha.22](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.21...v1.0.0-alpha.22) (2026-03-06)


### Bug Fixes

* **scheduler:** improve rollup backfill throughput ([4c7f19e](https://github.com/yassinebenameur/probara/commit/4c7f19ec9e90ca8077be444d1fc51d7a4621a973))

# [1.0.0-alpha.21](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.20...v1.0.0-alpha.21) (2026-03-06)


### Bug Fixes

* stabilize rollup analytics timestamps ([38ce144](https://github.com/yassinebenameur/probara/commit/38ce1441cbae0fd45b90a8ab31058e7c1e243c23))

# [1.0.0-alpha.20](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.19...v1.0.0-alpha.20) (2026-03-06)


### Bug Fixes

* fix dashboard panel sizing and status page links ([ec663da](https://github.com/yassinebenameur/probara/commit/ec663da651abd6b03790e9320a71543e644b250d))
* fix dashboard ui ([1435755](https://github.com/yassinebenameur/probara/commit/143575548c542b268ddc12128ea67571e917ea60))


### Features

* rollups ([8dc1546](https://github.com/yassinebenameur/probara/commit/8dc154610e6b0281e8b7682d0e480d3cfb6993f8))

# [1.0.0-alpha.19](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.18...v1.0.0-alpha.19) (2026-03-05)


### Bug Fixes

* **web:** resolve status page URLs from runtime origin and public_url ([9841c2a](https://github.com/yassinebenameur/probara/commit/9841c2aef7e9f4da5362e670d0b383637d063f77))


### Features

* add tenant data retention settings with 0-as-unlimited and daily cleanup ([1e00122](https://github.com/yassinebenameur/probara/commit/1e001222c533ada7c0fee87930cfbc3ebed0461c))
* **monitors:** add grpc monitor type with health-check execution, import support, UI integration, and unit tests ([9de1b88](https://github.com/yassinebenameur/probara/commit/9de1b88742bb13f14449553297e9b852e472ab9a))
* **monitors:** add monitor cloning and group management updates ([31253c8](https://github.com/yassinebenameur/probara/commit/31253c813999b5be89b2eac061d12a03790faaed))

# [1.0.0-alpha.18](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.17...v1.0.0-alpha.18) (2026-03-03)


### Bug Fixes

* improve dashboard/detail defaults and agent install public backend url ([a4f1dab](https://github.com/yassinebenameur/probara/commit/a4f1dabeca64a54ddb53d338f03bf5632936880d))

# [1.0.0-alpha.17](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.16...v1.0.0-alpha.17) (2026-03-03)


### Bug Fixes

* **performance:** add db indexes ([503e207](https://github.com/yassinebenameur/probara/commit/503e207dcd8737622fac3f5994d9281459a9fd97))

# [1.0.0-alpha.16](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.15...v1.0.0-alpha.16) (2026-03-02)


### Bug Fixes

* api url ([6d199da](https://github.com/yassinebenameur/probara/commit/6d199da14943a6e9c4e7b15d0a74ca94053ee111))

# [1.0.0-alpha.15](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.14...v1.0.0-alpha.15) (2026-03-01)


### Bug Fixes

* build ([5f91ff6](https://github.com/yassinebenameur/probara/commit/5f91ff60ac0725d370ec01dcc58f44d07a9af229))

# [1.0.0-alpha.14](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.13...v1.0.0-alpha.14) (2026-03-01)


### Bug Fixes

* monitor detail design ([bf16d9c](https://github.com/yassinebenameur/probara/commit/bf16d9c6bafb9bf8bbfa26e7aa5ebd9bcc0fc8da))
* ui of http monitor ([2b93d38](https://github.com/yassinebenameur/probara/commit/2b93d38ae51baf2f9962ee224f9d5ec29ff98955))
* ui of http monitor form ([e8ad657](https://github.com/yassinebenameur/probara/commit/e8ad6575318bc7b9bacdaf52bfea58803f284903))
* ui of other monitors ([8d8cf84](https://github.com/yassinebenameur/probara/commit/8d8cf847ff7957d03b9178b9216e5ec204d007c3))

# [1.0.0-alpha.13](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.12...v1.0.0-alpha.13) (2026-02-21)


### Bug Fixes

* errors in app schedules are ignored ([8e8553d](https://github.com/yassinebenameur/probara/commit/8e8553dcae58009650a0b40f26abe68e367c037d))

# [1.0.0-alpha.12](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.11...v1.0.0-alpha.12) (2026-02-21)


### Bug Fixes

* dashboard data ([9c08fc9](https://github.com/yassinebenameur/probara/commit/9c08fc963e7be22f5674cef7609c5b495fd3a454))

# [1.0.0-alpha.11](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.10...v1.0.0-alpha.11) (2026-02-19)


### Bug Fixes

* dashboard load all data ([6d9c812](https://github.com/yassinebenameur/probara/commit/6d9c812b943b86fb419c6b7d5d966f1a54dc4bfd))

# [1.0.0-alpha.10](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.9...v1.0.0-alpha.10) (2026-02-17)


### Bug Fixes

* frontend not forwarding auth cookies ([4ca6bcb](https://github.com/yassinebenameur/probara/commit/4ca6bcb9230c98731f3715bc25d104ec792e83e0))

# [1.0.0-alpha.9](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.8...v1.0.0-alpha.9) (2026-02-17)


### Bug Fixes

* create tenant with first admin ([caa2ce6](https://github.com/yassinebenameur/probara/commit/caa2ce6f6b3692a6fa40d94345a2d07eff66aef2))

# [1.0.0-alpha.8](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.7...v1.0.0-alpha.8) (2026-02-17)


### Bug Fixes

* api proxy target ([1c9f444](https://github.com/yassinebenameur/probara/commit/1c9f444cf0377ae38f9288c0672c71c9e78f418b))

# [1.0.0-alpha.7](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.6...v1.0.0-alpha.7) (2026-02-17)


### Bug Fixes

* api routing ([cff1c8c](https://github.com/yassinebenameur/probara/commit/cff1c8c5d9976eadf0191e8489ca8dc5e780b3d3))

# [1.0.0-alpha.6](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.5...v1.0.0-alpha.6) (2026-02-17)


### Bug Fixes

* frontend build ([715f71d](https://github.com/yassinebenameur/probara/commit/715f71d040225058d01341ec745d23b0f1edd4ba))

# [1.0.0-alpha.5](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.4...v1.0.0-alpha.5) (2026-02-17)


### Bug Fixes

* frontend publish ([07fd13c](https://github.com/yassinebenameur/probara/commit/07fd13cd58ea2d62b048452e0050321e012d9070))

# [1.0.0-alpha.4](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.3...v1.0.0-alpha.4) (2026-02-17)


### Bug Fixes

* release frontend, migrations ([5c70541](https://github.com/yassinebenameur/probara/commit/5c7054145a043e5359af61f1c7e3b081953b837f))

# [1.0.0-alpha.3](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.2...v1.0.0-alpha.3) (2026-02-17)


### Bug Fixes

* release ([ead1611](https://github.com/yassinebenameur/probara/commit/ead161108cc45c0c2a0ea87b35385c3e8a034b64))

# [1.0.0-alpha.2](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.1...v1.0.0-alpha.2) (2026-02-17)


### Bug Fixes

* change chart name ([5fac3a0](https://github.com/yassinebenameur/probara/commit/5fac3a011efda8824c745be28f1d357192bfbf38))
* change chart name ([806f4be](https://github.com/yassinebenameur/probara/commit/806f4be0532d6c5fe863e9443a7408adc449783a))

# 1.0.0-alpha.1 (2026-02-16)


### Features

* init project ([d388e3c](https://github.com/yassinebenameur/probara/commit/d388e3c06998e9953133b19bcc8cf443ac15d472))
