# [1.0.0-alpha.39](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.38...v1.0.0-alpha.39) (2026-05-21)


### Bug Fixes

* **status-page:** default public range to 24h ([340c60a](https://github.com/yassinebenameur/probara/commit/340c60ac5be38417b68f1aec06e054f696add4ab))
* **web:** repair auth proxy and settings tag menu ([8ee5173](https://github.com/yassinebenameur/probara/commit/8ee517395b993c5000d128e78661bbd3556dcf69))


### Features

* **dashboard:** add DashboardGroup model and extend summary response ([6acc155](https://github.com/yassinebenameur/probara/commit/6acc1557e517c86405731498d57844dbf0ef29a3))
* **dashboard:** aggregate tag-based groups in summary response ([fbd3e55](https://github.com/yassinebenameur/probara/commit/fbd3e55f57f1985b031769d624dab53632584b47))
* **dashboard:** expandable group rows with lazy sparkline ([38fa079](https://github.com/yassinebenameur/probara/commit/38fa07906854e35f371dbefb84d36a44ba567772))
* **dashboard:** GET /dashboard/group-sparkline route ([36857e3](https://github.com/yassinebenameur/probara/commit/36857e3a3696799a2653a047432a9e4e8860c35a))
* **dashboard:** per-group sparkline service method ([76b0839](https://github.com/yassinebenameur/probara/commit/76b08392e9d981a2f77c46356dd8d6b615fa54f7))
* **dashboard:** real service-groups panel with empty-state CTA ([39df544](https://github.com/yassinebenameur/probara/commit/39df544fbeb86fd4c3ba39b95a884def1e29768c))
* **db:** add dashboard_group_tags column to tenants ([50e8445](https://github.com/yassinebenameur/probara/commit/50e8445b1368470c5fedbdfbc12493a95a6415ab))
* **models:** add dashboard_group_tags to tenant settings ([025a222](https://github.com/yassinebenameur/probara/commit/025a2222f1f1924caa0994ed1c33154a320ac624))
* **settings:** add dashboard groups tag picker ([11d6619](https://github.com/yassinebenameur/probara/commit/11d66192918504e858536b5b7f4b420b09a1938d))
* **tenants:** accept dashboard_group_tags in PATCH settings ([8fc5ea8](https://github.com/yassinebenameur/probara/commit/8fc5ea83cc226ae99bb8af0b5e3ca425e0474be7))
* **tenants:** persist dashboard_group_tags ([8bc9362](https://github.com/yassinebenameur/probara/commit/8bc936260c084d7799e447fd9560da16565f2c15))
* **validation:** validate dashboard_group_tags shape ([d11c5b4](https://github.com/yassinebenameur/probara/commit/d11c5b4145fa9bbcaed47591399660ebb76d1f35))
* **web:** dashboard group types + sparkline api client ([3b4d7a8](https://github.com/yassinebenameur/probara/commit/3b4d7a87ab48a22208a4648ed4bd90076a4df296))

# [1.0.0-alpha.38](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.37...v1.0.0-alpha.38) (2026-04-24)


### Features

* **dashboard:** split heavy overview loading ([99e68cc](https://github.com/yassinebenameur/probara/commit/99e68cc3a55ef70e78bfef5cb65cdc363901a34c))

# [1.0.0-alpha.37](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.36...v1.0.0-alpha.37) (2026-04-23)


### Bug Fixes

* **api:** align incident list and timeline shape ([143cae6](https://github.com/yassinebenameur/probara/commit/143cae6e7a200507e04e47d7070cdce4fd1d6b91))
* **api:** avoid incident no-op audit entries ([d400d64](https://github.com/yassinebenameur/probara/commit/d400d64fc8b5c1b052092ecb8ecb820fed6b2803))
* **api:** clean incident publication monitor links ([36ac286](https://github.com/yassinebenameur/probara/commit/36ac28645f7a1b93bf49c425c481bccd15dcb35b))
* **api:** cover incident service error mapping ([74aa04d](https://github.com/yassinebenameur/probara/commit/74aa04db4922a655f8b10cf97ff2d100a08723db))
* **api:** expose incident timeline entry writes ([58046a2](https://github.com/yassinebenameur/probara/commit/58046a26ce5990c7458f9e7b7e494499b360801b))
* **api:** harden incident transitions and updates ([4407be6](https://github.com/yassinebenameur/probara/commit/4407be612ca1821a4f91b6b2758b83f37c419d1b))
* **api:** make incident publish idempotent ([08e58b6](https://github.com/yassinebenameur/probara/commit/08e58b68d7193bf796105db0c6e581cbe412e34d))
* **api:** narrow incident transition logging ([f3c01cf](https://github.com/yassinebenameur/probara/commit/f3c01cf41fd9e4d681ef622a1c3a20f49a579088))
* **api:** tighten incident handler error paths ([6d4ccec](https://github.com/yassinebenameur/probara/commit/6d4ccecce773590195e069bb35bc63b465adb824))
* **queue:** recreate stale check job consumers ([af31269](https://github.com/yassinebenameur/probara/commit/af312691036a9e7f36b1122d5cd0b7f3ce643a6b))
* **status-page:** preserve public page state on live refresh ([30c7386](https://github.com/yassinebenameur/probara/commit/30c7386f3b0138f692c2eb568d6124ab7dab6b28))
* **status-page:** sync uptime ranges and status copy ([b9ad538](https://github.com/yassinebenameur/probara/commit/b9ad538715343bdc64f2bc0c28b7bacb59d356a6))


### Features

* **api:** add core incident domain service ([48354d0](https://github.com/yassinebenameur/probara/commit/48354d0b513b8273d7ee084af8c10f9ef41810a8))
* **api:** add incident linking and publication flows ([bf2a9d9](https://github.com/yassinebenameur/probara/commit/bf2a9d947f9e83fc958fd26623d81b231693acff))
* **api:** expose core incident endpoints ([d5d2196](https://github.com/yassinebenameur/probara/commit/d5d2196e9e51d0d55fcd1f7f162e3553ec8c2913))
* **incidents:** add metadata and contextual creation ([9fa3b8a](https://github.com/yassinebenameur/probara/commit/9fa3b8a798c63039a44a6b998e0f2e5a5ee23895))
* **incidents:** finish MVP implementation ([0ad08c8](https://github.com/yassinebenameur/probara/commit/0ad08c8fe5bc27c3dfda0fc0a75ed3238d235f8a))
* **status-page:** support incident-only refresh events ([03065cf](https://github.com/yassinebenameur/probara/commit/03065cf9881098d39f33f5d8a34699053efad92a))

# [1.0.0-alpha.36](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.35...v1.0.0-alpha.36) (2026-03-26)


### Bug Fixes

* **import:** sync portable monitor service test mock ([ddc368c](https://github.com/yassinebenameur/probara/commit/ddc368cb48af298cc6fb68ea9ae763d6871f7cb7))


### Features

* **status-page:** redesign public page rendering and scoped live updates ([b708ace](https://github.com/yassinebenameur/probara/commit/b708ace93ee5c515a8bd13beead0ef20ae86ecbf))

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
