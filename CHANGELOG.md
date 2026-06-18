# [1.0.0-alpha.56](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.55...v1.0.0-alpha.56) (2026-06-18)


### Bug Fixes

* **agent:** install Linux agent as a root system service ([da5bae0](https://github.com/yassinebenameur/probara/commit/da5bae0c093790c0ec012461cf538656aba8f207))


### Features

* **alerts:** per-group alert roll-up (per-monitor vs one group alert) ([396a26f](https://github.com/yassinebenameur/probara/commit/396a26f5bd63962aa36a988c71774bd37dad23dd))
* **host-metrics:** enterprise host metrics — richer collection, charts & threshold alerts ([58d19fa](https://github.com/yassinebenameur/probara/commit/58d19fa295750f81155e3c69b29e7cad95453e9f))
* **status-page:** reveal full kiosk tile text in hover popover ([c525581](https://github.com/yassinebenameur/probara/commit/c525581d7dc33cf4768c9fd863b4b476a9008ae2))

# [1.0.0-alpha.55](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.54...v1.0.0-alpha.55) (2026-06-15)


### Bug Fixes

* **alerts:** scan latency anomaly columns in remaining alert read paths ([ba45639](https://github.com/yassinebenameur/probara/commit/ba456398490be223aee3f2e4c93a78ede2d2761d))
* **monitors:** advance state machine for agent and push results ([b5a457e](https://github.com/yassinebenameur/probara/commit/b5a457efeab7322cd9f2071fc5deef0319298852))
* **web:** add required notification fields to monitor-list test fixture ([9c5497b](https://github.com/yassinebenameur/probara/commit/9c5497b11861cd8bdbd153e49954cd1479a9ec9e))
* **web:** align username/password inputs in database monitor forms ([095e54e](https://github.com/yassinebenameur/probara/commit/095e54ee13801219b8f869dfa27f4b168c2e61bc))


### Features

* **alerts:** latency anomaly detection to catch degradation before hard-down ([6100c30](https://github.com/yassinebenameur/probara/commit/6100c30c53a2625883ed804418591935c38081bf))
* **dependencies:** AI-suggested dependencies from co-firing alerts, tags, groups ([5bb5ba5](https://github.com/yassinebenameur/probara/commit/5bb5ba51b448bb4c53f0d5ad671454833848b377))
* **incidents:** AI root cause analysis with per-tenant configurable LLM ([7ca2ef0](https://github.com/yassinebenameur/probara/commit/7ca2ef0aa06b1d44363a2bf567d6c4dac54b5faf))
* **monitors:** add Redis, PostgreSQL, and MongoDB monitor types with encrypted config secrets ([6522dbc](https://github.com/yassinebenameur/probara/commit/6522dbc87bd064ecd81ca1387376c906f1097152))
* **monitors:** add tcp monitor type ([bef0bf2](https://github.com/yassinebenameur/probara/commit/bef0bf2c3c54ed67487667b15e56af94497a1f41))
* **monitors:** dependency graph with dependency-aware alert annotations ([7a42e95](https://github.com/yassinebenameur/probara/commit/7a42e9501f5efc1defcdddea90b60fdffdcc4e5a))
* **monitors:** maintenance windows with alert suppression and snooze ([1989514](https://github.com/yassinebenameur/probara/commit/19895141e625300da0f3db2309672bae91bbfe70))
* **monitors:** maintenance windows with alert suppression and snooze ([5387eaa](https://github.com/yassinebenameur/probara/commit/5387eaac0820d815a9cfa595b950f5b2139cb431))
* **monitors:** RabbitMQ type, test-connection flow, richer DB assertions, key rotation ([51c13ae](https://github.com/yassinebenameur/probara/commit/51c13aec1967efe81ae7ab7c976da9c2acf9c5cd))

# [1.0.0-alpha.54](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.53...v1.0.0-alpha.54) (2026-06-11)


### Bug Fixes

* **dashboard:** polish dashboard UI and fix chart axis rendering ([369e50b](https://github.com/yassinebenameur/probara/commit/369e50b6a188e3703800cccd0f758beff47cbf5d))
* **monitors:** pin detail panel while scrolling and make list responsive ([93ec020](https://github.com/yassinebenameur/probara/commit/93ec0209687a2f96b69a2d7c9a191ae7146ff33f))
* **status-pages:** declutter and polish the status page form ([a35aa2e](https://github.com/yassinebenameur/probara/commit/a35aa2e68a2d9f0949a487bc50debaa9cc2bead5))
* **web:** unify toasts, confirm dialogs, and fix clipped group actions menu ([28a8a7b](https://github.com/yassinebenameur/probara/commit/28a8a7ba93aa0b456fcde72d9757244a0f4e273b))
* **worker:** make synthetic browser fill work on Chrome 149; bump CI actions to node24 ([f574904](https://github.com/yassinebenameur/probara/commit/f5749042a9997dc7a81784b82c88a56288739069))


### Features

* **monitors:** group-aware filtering and polished group UI ([7a0f214](https://github.com/yassinebenameur/probara/commit/7a0f214ca8f4aef784933a77cb803d088d59dc15))
* **monitors:** redesign monitor form type picker and unify form layout ([5492194](https://github.com/yassinebenameur/probara/commit/5492194408d9356bce5df695ba7995e23839e871))
* **status-page:** collapsible sections, denser compact/kiosk modes, refined bars ([dcb9ce9](https://github.com/yassinebenameur/probara/commit/dcb9ce970a99b852a7af5c153ffcbd2e0dbeaae8))

# [1.0.0-alpha.53](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.52...v1.0.0-alpha.53) (2026-06-10)


### Bug Fixes

* **alerter:** restore policy evaluator build ([fedea12](https://github.com/yassinebenameur/probara/commit/fedea12ac5bd3719fbdf6d06d9257a8d4db9c51d))

# [1.0.0-alpha.52](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.51...v1.0.0-alpha.52) (2026-06-10)


### Bug Fixes

* **alerter:** resolve alerts only after an observed success, not when the failure window empties ([4be0f78](https://github.com/yassinebenameur/probara/commit/4be0f78c7f5e612e76da2f68f918642dc893f23b))
* **alerter:** resolve alerts only after an observed success, not when the failure window empties ([1f38cb1](https://github.com/yassinebenameur/probara/commit/1f38cb1ed8e1ebb82911571a432feba7acad8bd9))
* **analytics:** whole-window per-monitor SLA for rollup ranges (D2) ([9e5f859](https://github.com/yassinebenameur/probara/commit/9e5f8598e4f24cc2189ab79beb42e3450152851a))
* **api:** map notification-settings DB errors to 500, reject unknown channels ([a98a686](https://github.com/yassinebenameur/probara/commit/a98a6869a1ae5acbda8aaa8d68e531b167123ba6))
* **api:** surface policy-less lifecycle alerts in all alert read paths ([cf5a817](https://github.com/yassinebenameur/probara/commit/cf5a8170408b10311718c815838e38d7ee6445ab))
* **helm:** wire NATS_URL into status-page deployment ([ca1f451](https://github.com/yassinebenameur/probara/commit/ca1f451a67c831c9e5a5ee0bed9003a403e6104d))
* **queue:** reconnect to NATS forever instead of giving up after 2 minutes ([bbc835e](https://github.com/yassinebenameur/probara/commit/bbc835e18fd0f6a794baa0e75803bf2e766ca57e))
* **queue:** reconnect to NATS forever instead of giving up after 2 minutes ([51e2404](https://github.com/yassinebenameur/probara/commit/51e2404b567b5378292458ebc0838144f44170c5))
* **scheduler,dashboard:** broaden transient rollup error classes; clamp failure attribution ([70ece64](https://github.com/yassinebenameur/probara/commit/70ece64cd25794bdf32491e5ba0756a27811e826))
* **scheduler:** treat schema-lag errors as transient, not row poison ([3e0b2e4](https://github.com/yassinebenameur/probara/commit/3e0b2e4cf52d0e01906878be0a50d305de48cc6d))
* **status-page,metrics:** evict expired render-cache entries; sanitize metric subsystem names ([05b01f5](https://github.com/yassinebenameur/probara/commit/05b01f57f59dd2f6a228850695f2eab2392f4426))
* **status-page:** bound slug throttler memory; document clock split ([0c48f55](https://github.com/yassinebenameur/probara/commit/0c48f5585e18d015cd2c0cad1c68b3bf4f054da2))
* **web:** honest worst-case detection hint; warn on bulk mute ([941aaa6](https://github.com/yassinebenameur/probara/commit/941aaa6ec86ff347660fde85653b8aff946f378d))
* **worker,alerter:** fail liveness once NATS connection is permanently closed ([77cb483](https://github.com/yassinebenameur/probara/commit/77cb4838ef6ed0ae29608e73430dff2da18408c5))
* **worker,alerter:** fail liveness once NATS connection is permanently closed ([e78c8c3](https://github.com/yassinebenameur/probara/commit/e78c8c33e1b1179f3b8828f13fc7bf43cc5e84cd))


### Features

* **alerter:** auto-create incidents from lifecycle alerts (policy-less) ([6a908dd](https://github.com/yassinebenameur/probara/commit/6a908ddabcba4a6a9d5fa7bcf662b6e81ce36e0a))
* **alerter:** transition-driven alert lifecycle with per-channel escalation; retire window-based evaluator ([859bf08](https://github.com/yassinebenameur/probara/commit/859bf08d763f85d22e766d301c4f7e9f5b90b001))
* **api:** include channel name/type in monitor notification channel assignments ([f853366](https://github.com/yassinebenameur/probara/commit/f853366ec872dafeaacc2231691b315714adf998))
* **api:** monitor sensitivity + notification routing fields, bulk alerting endpoint ([fe4eaa5](https://github.com/yassinebenameur/probara/commit/fe4eaa5e4d678e58aa75fac94ec319f751803e42))
* **api:** retire alert-policy endpoints (410 Gone) ([bacdda2](https://github.com/yassinebenameur/probara/commit/bacdda271ebc431abf07a4bf952e84bbe831b347))
* **api:** workspace notification settings endpoints ([5d0d178](https://github.com/yassinebenameur/probara/commit/5d0d178ce52d99644c3c48d94cbe669fe88c0717))
* **dashboard:** derive monitor health from persisted state machine (D1) ([b49ff00](https://github.com/yassinebenameur/probara/commit/b49ff00e965a408bebb6510f22864cd4ce12ed08))
* **db:** add monitor state machine and sensitivity columns ([87e2afb](https://github.com/yassinebenameur/probara/commit/87e2afb24a9de7d5ae1d8858deb1aedd1eff323d))
* **db:** notification routing tables, workspace alert settings, policy backfill ([1391806](https://github.com/yassinebenameur/probara/commit/13918061ed5d400d1eb53148e7ab65573ec0fc2c))
* **monitorstate:** pure state-machine transition logic ([94c2b37](https://github.com/yassinebenameur/probara/commit/94c2b373840bd1bafe46775c36291f9ac6dd1372))
* **scheduler:** fast-recheck suspect monitors at min(interval, 20s) ([8661f6d](https://github.com/yassinebenameur/probara/commit/8661f6d0515268ca465b922284d6e8226d5da09c))
* **scheduler:** skip poisoned rollup rows; track error_checks in rollups ([3e29727](https://github.com/yassinebenameur/probara/commit/3e2972721d3ce86ce9565c666aeb1e2418f34b97))
* **scripts:** idempotent hourly/daily rollup backfill ([63414b7](https://github.com/yassinebenameur/probara/commit/63414b7b52801ac2e011749b854927c4f5609ab0))
* **status-page:** harden SSE subscriber (no-client exit, slug cache, per-slug throttle) ([064eb52](https://github.com/yassinebenameur/probara/commit/064eb526601a9a7a312239ef2bc00d756943862d))
* **status-page:** per-slug render cache with ETag/304 and SSE invalidation ([bc66a82](https://github.com/yassinebenameur/probara/commit/bc66a82d1a9e515a8ba1fa006dd65839d047c18b))
* **status,dashboard:** derive monitor status from persisted state machine ([c639de8](https://github.com/yassinebenameur/probara/commit/c639de893cb797d4735e1a09cd675a3dcfaaff36))
* **web:** alerting section in group/agent/push/sip/grpc monitor forms ([bad8198](https://github.com/yassinebenameur/probara/commit/bad81982cc8ba8249252a4e03a219e8370a19651))
* **web:** bulk edit alerting; remove alert-policies UI ([70db79e](https://github.com/yassinebenameur/probara/commit/70db79eb63ce9055353e525511b55f2b1da7f647))
* **web:** monitor form alerting section (sensitivity + notifications picker) ([6e4c6d1](https://github.com/yassinebenameur/probara/commit/6e4c6d13ee743a6838e887149ca63d3bfdfcfe8a))
* **web:** shared ChannelPicker with inline create and escalation delays ([673f8cb](https://github.com/yassinebenameur/probara/commit/673f8cb8bee4f3970c2327625906e441d48516d9))
* **web:** types and client for notification settings and bulk alerting ([14c651e](https://github.com/yassinebenameur/probara/commit/14c651e6bd85cc8f17366ead890c5e8d89af4eb7))
* **web:** workspace notifications settings panel and day-0 nudge banner ([3941b97](https://github.com/yassinebenameur/probara/commit/3941b97380b5aa379b8f30a537cf7ac5bb123283))
* **worker:** advance monitor state machine transactionally with each result ([53f5559](https://github.com/yassinebenameur/probara/commit/53f55594d05efa5caeb2f7fc346d0a4740654b77))
* **worker:** publish status updates only on monitor state transitions ([ca24ae5](https://github.com/yassinebenameur/probara/commit/ca24ae50c3a0a8f0cf2634cea42a0a2cc236c469))


### Performance Improvements

* **dashboard,status-page:** pass window bounds as parameters so check_results scans use indexes ([60458d9](https://github.com/yassinebenameur/probara/commit/60458d9f042c1293d9d36c6fd18f7021fe8133ad))
* **dashboard:** bound recent-failures next-success probe to emitted rows ([10eb407](https://github.com/yassinebenameur/probara/commit/10eb40736c81d8ad822f98ea2c2373c83106b38c))
* **dashboard:** share rolling-24h aggregates via short-TTL cache ([2806c15](https://github.com/yassinebenameur/probara/commit/2806c1538d4c49eb6c548a38b27a014859877bfa))
* **scripts:** index-friendly bounds + retention warning for rollup backfill ([34c1330](https://github.com/yassinebenameur/probara/commit/34c133011caa7d6c6fa6fc08b9680bc739178231))
* **status-page:** parse template once; batch incident queries ([c38f219](https://github.com/yassinebenameur/probara/commit/c38f219e91582d22fff89f894b893dc6d205acb8))

# Unreleased — Alerting UX Revamp


### BREAKING CHANGES

* **alerting:** Alert policies have been replaced by workspace-level notification settings and per-monitor channel routing. Existing policies were automatically migrated: each policy's channel set becomes either the workspace default (if it was the most common) or a per-monitor custom list. Policies with `failure_threshold=1` are migrated to sensitivity N=1 (alert on first failure); all others to N=2 — review any monitors where the threshold should differ. The `/alert-policies` API endpoints have been removed and now return **410 Gone**. Callers must migrate to `GET/PUT /v1/notification-settings` (workspace defaults) and the per-monitor `notification_channels` / `consecutive_failures_threshold` fields.
* **alerting:** One open alert per monitor is now enforced. Any monitor that had multiple simultaneous active alerts will have had extras closed by the migration; the new one-alert-per-outage model means duplicate triggers are silently dropped rather than stacked.


### Features

* **alerting:** Monitor state machine (unknown → up → suspect → down) replaces the old window-based evaluator. While a monitor is `suspect`, the scheduler issues fast 20-second rechecks before confirming `down`, reducing false alerts on transient blips.
* **alerting:** Per-channel escalation delays and workspace-level reminder cadence: configure how long to wait before notifying each channel, and how often to re-notify on an ongoing outage.
* **alerting:** Bulk "Edit alerting" action on the monitors table allows setting channels and sensitivity across many monitors at once.
* **web:** Day-0 nudge banner prompts workspaces with no notification channels configured to complete setup before their first alert.
* **api:** New `GET /v1/notification-settings` and `PUT /v1/notification-settings` endpoints expose workspace-wide alert defaults (channels, reminder interval, auto-incident creation toggle).
* **api:** Monitor create/update now accepts `notification_channels` (per-monitor channel override list) and `consecutive_failures_threshold` (sensitivity N).


### Changes

* **status-page:** Public status pages and the internal dashboard now derive monitor status from the persisted state-machine column (`current_state`). A monitor in `suspect` state is shown as operational on public pages to avoid premature customer-visible incidents.
* **dashboard:** Problem-monitors panel reflects the state-machine `down` state rather than raw consecutive failure counts.


### Operations

* **deploy:** The policy→channel-routing backfill in migration `000045` should be validated against a production-like database snapshot before deploying to production. Run the migration in a dry-run or staging environment first and spot-check that channels mapped correctly.
* **deploy:** The new alerter image must be rolled out **together** with the API, worker, and scheduler images. Deploying the new alerter with an OLD worker leaves all monitor states as `unknown`, so the state machine never transitions to `down` — **no alerts will fire (silent outage)**. The reverse is also unsafe: a new worker writing state-machine state with the old alerter's window-based evaluator will cause missed or duplicate alerts. The worker, scheduler, alerter, and API must all ship in the same deployment.

# [1.0.0-alpha.51](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.50...v1.0.0-alpha.51) (2026-06-10)


### Bug Fixes

* **alerter:** resolve alerts only after an observed success, not when the failure window empties ([1f38cb1](https://github.com/yassinebenameur/probara/commit/1f38cb1ed8e1ebb82911571a432feba7acad8bd9))
* **queue:** reconnect to NATS forever instead of giving up after 2 minutes ([51e2404](https://github.com/yassinebenameur/probara/commit/51e2404b567b5378292458ebc0838144f44170c5))
* **status-page:** use rollups for 24h monitor aggregates ([0a37427](https://github.com/yassinebenameur/probara/commit/0a37427a187774b6c0ea9190343660ddeedfc310))
* **worker,alerter:** fail liveness once NATS connection is permanently closed ([e78c8c3](https://github.com/yassinebenameur/probara/commit/e78c8c33e1b1179f3b8828f13fc7bf43cc5e84cd))

# [1.0.0-alpha.50](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.49...v1.0.0-alpha.50) (2026-06-02)


### Bug Fixes

* **status-page:** bound batched check result lookups ([a50a0b4](https://github.com/yassinebenameur/probara/commit/a50a0b4716f89009c217ced2b37cfb0f2566a1a2))

# [1.0.0-alpha.49](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.48...v1.0.0-alpha.49) (2026-06-02)


### Performance Improvements

* **dashboard:** bound 24h hourly raw scan ([d9a6940](https://github.com/yassinebenameur/probara/commit/d9a6940c88b0bfd7ff6b735a33402d1728bc395a))
* **status-page:** bound global 24h hourly scan ([751ebac](https://github.com/yassinebenameur/probara/commit/751ebac8c07246d85ece8c40aed8464457239412))

# [1.0.0-alpha.48](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.47...v1.0.0-alpha.48) (2026-06-01)


### Bug Fixes

* **web:** proxy agent binary downloads ([6c2a23f](https://github.com/yassinebenameur/probara/commit/6c2a23f60e7f985825d3967db25445b2e042e4fc))


### Performance Improvements

* **dashboard:** fix slow 24h summary via edge-range scan and dedupe ([f31f02e](https://github.com/yassinebenameur/probara/commit/f31f02e81ee5a128fbbdcfec187a5df0f320d43d))
* **status-page:** batch monitor loading to fix N+1 query timeout ([32a0f45](https://github.com/yassinebenameur/probara/commit/32a0f455b332c865b80b0d463ef5c10d34f5ebd9))

# [1.0.0-alpha.47](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.46...v1.0.0-alpha.47) (2026-05-31)


### Bug Fixes

* **dashboard:** restore 1h problem monitors window (regression from Task 10) ([eec9146](https://github.com/yassinebenameur/probara/commit/eec9146e8c9e3422778ee5448f0c55b9c41a0c1c))


### Features

* **dashboard:** add loadExactRolling24hSummary helper ([795d41e](https://github.com/yassinebenameur/probara/commit/795d41ecaaf97146a5663defe5faed0fea382a71))
* **dashboard:** add loadHourlyBucketSeries24h helper ([ce078e5](https://github.com/yassinebenameur/probara/commit/ce078e5eeca6550704c3d15d3739c1bee82b0049))


### Performance Improvements

* **dashboard:** derive Activity24h from hour-aligned bucket helper ([38ceef8](https://github.com/yassinebenameur/probara/commit/38ceef865573956a6443e560d29b7408c558b16c))
* **dashboard:** switch 24h problem monitors to exact-rolling helper ([72218aa](https://github.com/yassinebenameur/probara/commit/72218aa8063d9d3f7f81608732086da4f78cbe01))
* **dashboard:** use exact-rolling hourly rollup helper for 24h stats ([c365406](https://github.com/yassinebenameur/probara/commit/c365406f1091314f2ae2efdc61300f90232126ef))
* **dashboard:** use exact-rolling summary for 24h groups ([f6d4ad9](https://github.com/yassinebenameur/probara/commit/f6d4ad99c0c65198596f778862bbd867127adae7))
* **dashboard:** use hour-aligned bucket helper for 24h trend ([8072345](https://github.com/yassinebenameur/probara/commit/8072345a0b241a4279614da741d0906ac7eea38b))
* **dashboard:** use hour-aligned helper for 24h group sparkline ([a663eee](https://github.com/yassinebenameur/probara/commit/a663eee6d55fd2a7e54c0e0eaa484eb376d9c7f1))

# [1.0.0-alpha.46](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.45...v1.0.0-alpha.46) (2026-05-26)


### Bug Fixes

* align alerts mock SQL and import test mock with new interface ([e83a0b2](https://github.com/yassinebenameur/probara/commit/e83a0b23ba7babae94e6ba209d947d6b2cb80074))
* **monitors:** detach auto-incidents and filter tombstoned group members ([9dcdd65](https://github.com/yassinebenameur/probara/commit/9dcdd6568f1c26eb9dc002ae40e0afc673291cf4))
* **scheduler:** pin purger SQL to one connection; detach incidents on down migration ([fe60b75](https://github.com/yassinebenameur/probara/commit/fe60b754f015e6ddfc8171e0ef896748d33dd51e))
* **web:** resolve dashboard page.tsx merge conflicts ([7ff7a27](https://github.com/yassinebenameur/probara/commit/7ff7a2766679bda8f9402e777e82d632dcfb267c))


### Features

* **api:** add POST /monitors/bulk/delete for soft bulk delete ([37f7d3f](https://github.com/yassinebenameur/probara/commit/37f7d3fa636ea4970e6f816fca404205c8bb63b4))
* **config:** add monitor purge worker tunables ([5cffee5](https://github.com/yassinebenameur/probara/commit/5cffee5d69f26e849a0367ebca0f01d7c6b77513))
* **db:** add monitors.deleted_at and partial unique indexes for soft delete ([a22ce0d](https://github.com/yassinebenameur/probara/commit/a22ce0dd6b706c6d0eee1f64805c3a6242efc82b))
* filter soft-deleted monitors from every read and update site ([f690c1f](https://github.com/yassinebenameur/probara/commit/f690c1fbc8d7a986012efa628ad6c12608d2bd73))
* **models:** add DeletedAt to Monitor and select it in repo queries ([a84f3bf](https://github.com/yassinebenameur/probara/commit/a84f3bf4bcea29eabfb8adf8ff7fb25dba7ebd83))
* **monitors:** exclude tombstoned monitors from repository reads ([2502980](https://github.com/yassinebenameur/probara/commit/2502980c72d2cd755a558b2d41eded1daec651b0))
* **monitors:** soft delete via tombstone + BulkSoftDelete/HardDelete repo methods ([38303ee](https://github.com/yassinebenameur/probara/commit/38303ee7528024d1b9d81511fbcfb860992f4592))
* **scheduler:** add async purger for soft-deleted monitors ([c76051e](https://github.com/yassinebenameur/probara/commit/c76051e0b143b89cb07cfe6bee862e9f0afba6a5))
* **scheduler:** add per-batch debug logs to monitor purger ([9af3cd5](https://github.com/yassinebenameur/probara/commit/9af3cd5ad40d30fbd70276fedee33e67bc4c8d32))
* **scheduler:** skip soft-deleted monitors when fetching due work ([daa9797](https://github.com/yassinebenameur/probara/commit/daa97975796a928f100168c4345dbde19d035aab))

# [1.0.0-alpha.45](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.44...v1.0.0-alpha.45) (2026-05-25)


### Features

* **alerts:** introduce alert channel plugin system ([c225e76](https://github.com/yassinebenameur/probara/commit/c225e76390ea87e06405bac544d534329b159102))

# [1.0.0-alpha.44](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.43...v1.0.0-alpha.44) (2026-05-24)


### Bug Fixes

* **api:** honor PUBLIC_BASE_URL when building agent/push URLs ([9a1fba8](https://github.com/yassinebenameur/probara/commit/9a1fba8e15a529ef27301abb814ae0ee94328d4a))
* **ci:** update chromedp dependencies for Chrome loopback events ([ddc0d0d](https://github.com/yassinebenameur/probara/commit/ddc0d0d57890a765de017dfcd5db0c8175c916f9))

# [1.0.0-alpha.43](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.42...v1.0.0-alpha.43) (2026-05-23)


### Bug Fixes

* **monitors:** always return [] for empty MonitorIDsUpdated ([6a61fb1](https://github.com/yassinebenameur/probara/commit/6a61fb1531cc3bb40a44b8f2a09b95eefbea8998))
* **monitors:** exact-string error matching in bulk handler + add tests ([825748e](https://github.com/yassinebenameur/probara/commit/825748e07abde29663a1c562a48f350423f440d5))
* **status-page:** improve dark-mode contrast ([4367680](https://github.com/yassinebenameur/probara/commit/4367680fc663f06d3611df56581a0e3d2ef8918a))
* **web:** replace removed btn-* classes with Button component in bulk policy UI ([edf476e](https://github.com/yassinebenameur/probara/commit/edf476ea1d49fb92b953f7855c7aac41c2eba3b4))


### Features

* **monitors:** add bulk alert policy request/response types ([971154f](https://github.com/yassinebenameur/probara/commit/971154fdfd18ffd874f8bcd4ff15b7bd3bfdf432))
* **monitors:** add BulkAttachAlertPolicy and BulkDetachAlertPolicy repo methods ([6f46356](https://github.com/yassinebenameur/probara/commit/6f4635678b6b006a405d04c77a0685a37879839f))
* **monitors:** add BulkUpdateAlertPolicy HTTP handler ([99d05c1](https://github.com/yassinebenameur/probara/commit/99d05c1391ab6405a024a987d05f727d02e1f378))
* **monitors:** add BulkUpdateAlertPolicy service method ([45fe5b5](https://github.com/yassinebenameur/probara/commit/45fe5b527fa50062ffa2ea2e24a6ef8c0d9fd6bc))
* **monitors:** add VerifyMonitorsBelongToTenant repository method ([873bfe8](https://github.com/yassinebenameur/probara/commit/873bfe8b1431aa897a0671b8148215fa4d28c665))
* **monitors:** wire POST /v1/monitors/bulk/alert-policy route ([09ae9cb](https://github.com/yassinebenameur/probara/commit/09ae9cb648ccb9419d92e06a6a3f07596dc8c4fe))
* **web:** add BulkAttachPolicyDialog component ([4e03c42](https://github.com/yassinebenameur/probara/commit/4e03c420e80c6f2fd983755d57f6801cf23aa41e))
* **web:** add bulkUpdateMonitorAlertPolicy API client ([67d72fc](https://github.com/yassinebenameur/probara/commit/67d72fcf150a709c940462c58af1824004549b0d))
* **web:** wire BulkAttachPolicyDialog into alert policy detail page ([8febd56](https://github.com/yassinebenameur/probara/commit/8febd563dd03cf719b775bd48b7c35a531181aca))
* **web:** wire BulkAttachPolicyDialog into monitors list page ([5338fd4](https://github.com/yassinebenameur/probara/commit/5338fd4e304121ba93af79808815e9d0dd084a9b))

# [1.0.0-alpha.42](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.41...v1.0.0-alpha.42) (2026-05-22)


### Features

* **agent:** supervise installs and mark stale agents down ([c1dc4d1](https://github.com/yassinebenameur/probara/commit/c1dc4d1f80cd602e4ec46f0206cd2053d20b6b7a))
* **agent:** support remote service uninstall ([08c0a66](https://github.com/yassinebenameur/probara/commit/08c0a667bd3b659442fe46a8513855561c4d4350))
* **dashboard:** add hourly rollups for short ranges ([d65b587](https://github.com/yassinebenameur/probara/commit/d65b5872587927a6f8011fd533d3aa1705a545cb))
* **web/ui:** add FilterChip with selected state and count slot ([ea7c47b](https://github.com/yassinebenameur/probara/commit/ea7c47b9a4a6ebd6778d540863b17ac2e95451c5))
* **web/ui:** add FormActions bar with optional sticky mode ([f6aae5c](https://github.com/yassinebenameur/probara/commit/f6aae5c0678c2a325c13694b3916d613415a657e))
* **web/ui:** add FormCard outer container ([8d7ad54](https://github.com/yassinebenameur/probara/commit/8d7ad54b27868354b57866bad4c47706f0ce8353))
* **web/ui:** add FormSection container ([2b46662](https://github.com/yassinebenameur/probara/commit/2b46662f1e9a3a4640e4879cc4c0dcf56a72f07f))
* **web/ui:** add infoTip prop to FormField ([94c1fe9](https://github.com/yassinebenameur/probara/commit/94c1fe9370bac953ee9ff2a358a2f9eb8e2fd08d))
* **web/ui:** add PageHeader and Breadcrumb primitives ([88a2f89](https://github.com/yassinebenameur/probara/commit/88a2f89c8af185a7bad8257271508fa1f71d8fda))
* **web/ui:** add Pill primitive with success/danger/warning/info/neutral/tag tones ([6e43e94](https://github.com/yassinebenameur/probara/commit/6e43e94084405830d4440108b838479d0251776f))
* **web/ui:** extract InfoTip popover primitive with portal positioning ([c70b9fc](https://github.com/yassinebenameur/probara/commit/c70b9fcd9d95640410359c4d1f937cfa53be6622))
* **web/ui:** rewrite Button as ghost/accent/danger/subtle primitive ([ae3d2cc](https://github.com/yassinebenameur/probara/commit/ae3d2ccfd3f08a278c3f38178e9be28442b9473e))
* **web/ui:** rewrite EmptyState for dark theme ([90a1d0b](https://github.com/yassinebenameur/probara/commit/90a1d0bd843178fe8287722b57db36e3f89af384))

# [1.0.0-alpha.41](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.40...v1.0.0-alpha.41) (2026-05-21)


### Bug Fixes

* **dashboard:** handle null group attention state ([16b2807](https://github.com/yassinebenameur/probara/commit/16b280765e37e2e4623520581c0b12283dca1acc))

# [1.0.0-alpha.40](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.39...v1.0.0-alpha.40) (2026-05-21)


### Bug Fixes

* **import:** skip already imported monitors ([34a5128](https://github.com/yassinebenameur/probara/commit/34a51280a4a42c9d34415bd452a9d096de5b2839))
* **monitors:** sort grouped monitor lists alphabetically ([afb3276](https://github.com/yassinebenameur/probara/commit/afb3276777e80faa4b2ca89a004c599174513a63))

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
