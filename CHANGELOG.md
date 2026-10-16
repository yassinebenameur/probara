# 1.0.0 (2026-10-10)


### Bug Fixes

* **agent:** install Linux agent as a root system service ([5517159](https://github.com/yassinebenameur/probara/commit/55171594080f89dd194b0cb61c7fdc1f35c7432e))
* **alerter:** claim each notification atomically so replicas never send it twice ([5e8d578](https://github.com/yassinebenameur/probara/commit/5e8d578dd8b2254e9cda5b81dda1c2c6ac1ced5e))
* **alerter:** resolve alerts only after an observed success, not when the failure window empties ([03f6b58](https://github.com/yassinebenameur/probara/commit/03f6b5884416129b2741e981cee5458339837f57))
* **alerter:** resolve alerts only after an observed success, not when the failure window empties ([bb38b8e](https://github.com/yassinebenameur/probara/commit/bb38b8e934c3819964c30b09fc704f97c64ea4a1))
* **alerter:** resolve orphaned latency-anomaly alerts when no tenant has anomaly enabled ([d342318](https://github.com/yassinebenameur/probara/commit/d342318dcab20015a71951070268156f1cfe83b6))
* **alerter:** restore policy evaluator build ([220e3e3](https://github.com/yassinebenameur/probara/commit/220e3e319e75e82f464f5fef50771452bde47f99))
* **alerter:** tls_expiry cert query timed out and blocked all alert dispatch ([2e2a13c](https://github.com/yassinebenameur/probara/commit/2e2a13cc672ab631898bd5dc6cf638d5afa944b8))
* **alerts:** scan latency anomaly columns in remaining alert read paths ([6d0785b](https://github.com/yassinebenameur/probara/commit/6d0785bd96646f4c2900b47d3003eea034f57939))
* align alerts mock SQL and import test mock with new interface ([145efed](https://github.com/yassinebenameur/probara/commit/145efed72d22a558b921f23686822290021f3d04))
* **analytics:** whole-window per-monitor SLA for rollup ranges (D2) ([50a4453](https://github.com/yassinebenameur/probara/commit/50a4453ccd8de8cfd49ab4fdaa2fe1af913f080c))
* api proxy target ([e72df32](https://github.com/yassinebenameur/probara/commit/e72df32ae88d5c96e6f5fa4b02006a5c5c5574f1))
* api routing ([1ade65f](https://github.com/yassinebenameur/probara/commit/1ade65fd6f17ca68cf7d629da30941e182474aa2))
* api url ([67000e7](https://github.com/yassinebenameur/probara/commit/67000e7887b91a5fbdf4ac54d9817646e6c5a2a9))
* **api:** alert list time window keeps alerts that are still open ([2c34e01](https://github.com/yassinebenameur/probara/commit/2c34e01a5ea675a17f0d9478192f6490e603958d))
* **api:** align incident list and timeline shape ([ce2e2c5](https://github.com/yassinebenameur/probara/commit/ce2e2c547140dfb6abae763815f7c6cdd19adbf9))
* **api:** avoid incident no-op audit entries ([c5976c7](https://github.com/yassinebenameur/probara/commit/c5976c7843088683d1764024bf242ed5d8193b74))
* **api:** clean incident publication monitor links ([6624244](https://github.com/yassinebenameur/probara/commit/6624244822492cc5d11449aa52cbfeb8bfe9307a))
* **api:** cover incident service error mapping ([c0bcea1](https://github.com/yassinebenameur/probara/commit/c0bcea1a3af90e0861e5f740e6e5af7d622d75f4))
* **api:** expose incident timeline entry writes ([70d8f05](https://github.com/yassinebenameur/probara/commit/70d8f05bc5c48cfef24edb57caa27e80bd24357a))
* **api:** fan out alert events to all SSE pods ([c7243f1](https://github.com/yassinebenameur/probara/commit/c7243f1390a10b26467f734e849bcd47b3048a38))
* **api:** harden incident transitions and updates ([19fa94f](https://github.com/yassinebenameur/probara/commit/19fa94f20efbec7cdfaf34bffa624476883ed1e9))
* **api:** honor PUBLIC_BASE_URL when building agent/push URLs ([33fce93](https://github.com/yassinebenameur/probara/commit/33fce9369edc154c76c95b4822b43716a173b66c))
* **api:** make incident publish idempotent ([97b4da5](https://github.com/yassinebenameur/probara/commit/97b4da56ea39e0bb7e529ff8b6eb6c1c901b63ed))
* **api:** map notification-settings DB errors to 500, reject unknown channels ([67d09db](https://github.com/yassinebenameur/probara/commit/67d09db6e48e84920425cc61b9d02e30d842675c))
* **api:** narrow incident transition logging ([c3663b9](https://github.com/yassinebenameur/probara/commit/c3663b9eeed1c62c9c3962a970ef5456c54515ca))
* **api:** ship collector binaries in the api image and unbreak CI ([e71c473](https://github.com/yassinebenameur/probara/commit/e71c473c89bd85ec02c3614fc87d331a55747267))
* **api:** start alert SSE subscriber ([44f1a6c](https://github.com/yassinebenameur/probara/commit/44f1a6c94f2838a8cf3b5e1e2d9ef70e77bc10bf))
* **api:** stop rejecting agent monitor edits on timeout validation ([d6d663a](https://github.com/yassinebenameur/probara/commit/d6d663aa1500f6da98ceb2f0e4fcbf49103ddf91))
* **api:** surface policy-less lifecycle alerts in all alert read paths ([712f6c0](https://github.com/yassinebenameur/probara/commit/712f6c00ce7003047665008c38d4943330759575))
* **api:** tighten incident handler error paths ([2459c92](https://github.com/yassinebenameur/probara/commit/2459c92db3b0dd20052811793fef0c70d999abae))
* **api:** use core nats for alert SSE fanout ([ad0d17d](https://github.com/yassinebenameur/probara/commit/ad0d17de021e74c9fb586480231d04a8145fcee2))
* **api:** wire SMTP mailer so email channel tests stop failing ([160161a](https://github.com/yassinebenameur/probara/commit/160161ab956c1d437b31d14c04982abf4c0df55a))
* **auth:** stop concurrent refreshes from killing the session ([f7b2486](https://github.com/yassinebenameur/probara/commit/f7b24861050d6318fe880eaafcad8282c8ec750e))
* build ([b9329e7](https://github.com/yassinebenameur/probara/commit/b9329e760d85ab5e75c228b529dfd351bd50877e))
* change chart name ([b56bce8](https://github.com/yassinebenameur/probara/commit/b56bce86d9f163c3d66cb78867254bc01f07eaa1))
* change chart name ([806f4be](https://github.com/yassinebenameur/probara/commit/806f4be0532d6c5fe863e9443a7408adc449783a))
* **ci:** update chromedp dependencies for Chrome loopback events ([1b13aaf](https://github.com/yassinebenameur/probara/commit/1b13aafc9d7f26e16141cf642bdfa6e2c32b8114))
* create tenant with first admin ([50bda0d](https://github.com/yassinebenameur/probara/commit/50bda0df6b987311dc3a42778e3c9d1450e4858f))
* dashboard data ([e59fe39](https://github.com/yassinebenameur/probara/commit/e59fe3995fc59393b63541e727bfe0ac6b8f427f))
* dashboard load all data ([e120f72](https://github.com/yassinebenameur/probara/commit/e120f72175950a4e3bee5fcd7d7c4589e167caf7))
* **dashboard:** handle null group attention state ([5553bd3](https://github.com/yassinebenameur/probara/commit/5553bd3b6fef43ccbbce65ab46191df0ba5905ff))
* **dashboard:** polish dashboard UI and fix chart axis rendering ([c10b0db](https://github.com/yassinebenameur/probara/commit/c10b0dbc2ee1f82ff09a7243271f6d71744c377e))
* **dashboard:** restore 1h problem monitors window (regression from Task 10) ([5460d43](https://github.com/yassinebenameur/probara/commit/5460d43f2f0e4c322482d7d608b698f267aafe10))
* **dashboard:** wrap overflowing panel content ([ee4b1d5](https://github.com/yassinebenameur/probara/commit/ee4b1d5c9be03174511e70d2d8ee5a0fa86d2b32))
* **deploy:** align run-now check-job subject and wire scheduler secrets key ([6d0dbb9](https://github.com/yassinebenameur/probara/commit/6d0dbb918868c7765abd97a93928252a19e86433))
* **deps:** remediate runtime vulnerabilities ([e1b4ef0](https://github.com/yassinebenameur/probara/commit/e1b4ef03ea2cd3c1a5807a582856d083c07eb806))
* errors in app schedules are ignored ([419fd6e](https://github.com/yassinebenameur/probara/commit/419fd6e920bfd26cf065fa8eb50f43f1459ea03a))
* fix dashboard panel sizing and status page links ([84105f3](https://github.com/yassinebenameur/probara/commit/84105f3a76e873ca3e92642797ad76d32b530d28))
* fix dashboard ui ([67a4b89](https://github.com/yassinebenameur/probara/commit/67a4b895811ff329c108555b2689f58206aa8fb3))
* frontend build ([22039cc](https://github.com/yassinebenameur/probara/commit/22039cc900d2540591c23da3dbb72b93574ff433))
* frontend not forwarding auth cookies ([4460607](https://github.com/yassinebenameur/probara/commit/4460607c8db404084c17212be7cefa6e3dbbc713))
* frontend publish ([4c8a183](https://github.com/yassinebenameur/probara/commit/4c8a183243fc9ba4501005cc1b10d6ff899e26f4))
* **frontend:** show gaps for incomplete dashboard rollups ([5d9bfb1](https://github.com/yassinebenameur/probara/commit/5d9bfb1a3835845640d93f42b2100494b2ff2d00))
* **helm:** wire NATS_URL into status-page deployment ([30b7c93](https://github.com/yassinebenameur/probara/commit/30b7c93de3afb4d2b12027a74a71d00834d5916e))
* **import:** accept all registered monitor types in portable import ([6b54c9e](https://github.com/yassinebenameur/probara/commit/6b54c9e0fb5cb404b0615b1e817634054fc254f8))
* **import:** skip already imported monitors ([5d0e033](https://github.com/yassinebenameur/probara/commit/5d0e033fb34a556f0ce65410e15cc6c892f9a977))
* **import:** sync portable monitor service test mock ([1cd1e44](https://github.com/yassinebenameur/probara/commit/1cd1e44f9ead00f6d87c8470b0e6fd7557e8e53f))
* improve dashboard/detail defaults and agent install public backend url ([00ddf07](https://github.com/yassinebenameur/probara/commit/00ddf0708943dacb507d7549633a5ba9b68d9c1f))
* monitor detail design ([f21df18](https://github.com/yassinebenameur/probara/commit/f21df1875fa004570abad5f7b444966df3c623ff))
* **monitors:** advance state machine for agent and push results ([0c2601c](https://github.com/yassinebenameur/probara/commit/0c2601cea0d5e10a01e1a3e73ae7082f74e9d150))
* **monitors:** always return [] for empty MonitorIDsUpdated ([eeca2b4](https://github.com/yassinebenameur/probara/commit/eeca2b452bc4ec94a9463b3df8bb611d0ddcdb05))
* **monitors:** detach auto-incidents and filter tombstoned group members ([687d126](https://github.com/yassinebenameur/probara/commit/687d12655e2ddc64f6b2e44cdaa486f099c0fddd))
* **monitors:** exact-string error matching in bulk handler + add tests ([5f1701f](https://github.com/yassinebenameur/probara/commit/5f1701f17ee7e4987cf365875030b3b7e4fb91a2))
* **monitors:** pin detail panel while scrolling and make list responsive ([a9d2027](https://github.com/yassinebenameur/probara/commit/a9d202747bf97530b134e86dd994da69acf50c22))
* **monitors:** sort grouped monitor lists alphabetically ([5e32482](https://github.com/yassinebenameur/probara/commit/5e32482814a6f6d020de6b0b7d7fc60ae7948a4a))
* **monitorstate:** keep the state timeline monotonic when a writer loses the lock race ([195c2dc](https://github.com/yassinebenameur/probara/commit/195c2dcf496feb69228b98fe13c812673f746996))
* **notifications:** close four delivery bugs found in review ([e9a6e8b](https://github.com/yassinebenameur/probara/commit/e9a6e8ba2a55d7fe1bfb6eb3b113ba11e379fa0a))
* **notifications:** order acks after late triggers, unblock alerter claims ([e46b7c1](https://github.com/yassinebenameur/probara/commit/e46b7c15f1001321de0e64bdc36d70f114e00e86))
* **notifications:** serialize paging delivery with resolution; keep SMS Retry-After ([96605e9](https://github.com/yassinebenameur/probara/commit/96605e97efea50afb8d6decebd33416181583105))
* **performance:** add db indexes ([c741724](https://github.com/yassinebenameur/probara/commit/c741724f3fdbd8119afb8d3c00cc753456cea211))
* **platform:** secure credentials and improve monitoring reliability ([08ae85f](https://github.com/yassinebenameur/probara/commit/08ae85f21ae6b2764dbb6e03856a448dd82bd168))
* **queue:** reconnect to NATS forever instead of giving up after 2 minutes ([5618fb5](https://github.com/yassinebenameur/probara/commit/5618fb5fed0a28ab3cec029fbab799073c72737f))
* **queue:** reconnect to NATS forever instead of giving up after 2 minutes ([8621282](https://github.com/yassinebenameur/probara/commit/8621282b038ab8ef87e85bcd00e2d1cd8f92dac5))
* **queue:** recreate stale check job consumers ([2a87abd](https://github.com/yassinebenameur/probara/commit/2a87abd05af14b59de86147c91b38673c8c5fbc3))
* **queue:** retain check jobs as work queue ([ea5a842](https://github.com/yassinebenameur/probara/commit/ea5a842bb3cfe617ee79a4c22aa9da26ad1c5742))
* release ([c0f609d](https://github.com/yassinebenameur/probara/commit/c0f609d42254483b83953038e410b5cb629add37))
* release frontend, migrations ([666d5c5](https://github.com/yassinebenameur/probara/commit/666d5c59eb544e25d265282facd3d43499e60f2f))
* **scheduler,dashboard:** broaden transient rollup error classes; clamp failure attribution ([066f240](https://github.com/yassinebenameur/probara/commit/066f24065edc6758d7eee4131beee246056fca13))
* **scheduler:** improve rollup backfill throughput ([1f68752](https://github.com/yassinebenameur/probara/commit/1f68752d2d43dea3d2dc6a1822c2f8f22a8d94ae))
* **scheduler:** keep raw check_results 30 days, serve longer ranges from rollups ([663b275](https://github.com/yassinebenameur/probara/commit/663b27521fe4fa9dbbea842d04460095180ba619))
* **scheduler:** log rollup backfill progress ([090ae82](https://github.com/yassinebenameur/probara/commit/090ae8270cd482a1d5f4300364b6996d184e2c2c))
* **scheduler:** pin purger SQL to one connection; detach incidents on down migration ([19f376b](https://github.com/yassinebenameur/probara/commit/19f376ba164f293eda03a2d315885931382fe898))
* **scheduler:** replace the rollup cursor with a dirty-bucket ledger ([1dbc041](https://github.com/yassinebenameur/probara/commit/1dbc0413509384d5ee6752b8e9c6cd666b3d9628)), closes [hi#water](https://github.com/hi/issues/water)
* **scheduler:** retry result ingest indefinitely instead of dropping ([c15711e](https://github.com/yassinebenameur/probara/commit/c15711eb6e685c496e48b5a9568c295bed03553b))
* **scheduler:** treat schema-lag errors as transient, not row poison ([655d521](https://github.com/yassinebenameur/probara/commit/655d521c6dfbf079099e716de8a3d856794d7603))
* **secrets:** keep stored monitor secrets when an update omits the field ([5e85e41](https://github.com/yassinebenameur/probara/commit/5e85e4176d20052c9b78748ed8087e26a2752341))
* **security:** P1 review fixes — location worker auth, NATS auth, secret hardening ([9a9c793](https://github.com/yassinebenameur/probara/commit/9a9c793c276dfa3e683e466a82226a6fa565a42e))
* stabilize rollup analytics timestamps ([d64dc7a](https://github.com/yassinebenameur/probara/commit/d64dc7aca176e4b5bef48cc45c2dc5eaf52449ca))
* **status-page,metrics:** evict expired render-cache entries; sanitize metric subsystem names ([f05e613](https://github.com/yassinebenameur/probara/commit/f05e613ff7a951306fc55879f76c86cff3eb0963))
* **status-page:** align monitor titles with fixed-width type badges ([04e352e](https://github.com/yassinebenameur/probara/commit/04e352eaa09a3072248dcade61c9a145107f5738))
* **status-page:** bound batched check result lookups ([633d7ef](https://github.com/yassinebenameur/probara/commit/633d7efd275f9a02d77dce30ee81b1dff5c940bf))
* **status-page:** bound slug throttler memory; document clock split ([0535754](https://github.com/yassinebenameur/probara/commit/0535754b151789ba15e89bcaf2171a7f742ef1ff))
* **status-page:** default public range to 24h ([fa214f7](https://github.com/yassinebenameur/probara/commit/fa214f73fcb5c0949cb756a9a2ccad1793f99959))
* **status-page:** improve dark-mode contrast ([eb3168d](https://github.com/yassinebenameur/probara/commit/eb3168d8acc44653447856b3f804929888f8099e))
* **status-page:** make the visitor notification toggle actually work ([7107990](https://github.com/yassinebenameur/probara/commit/71079905148253d605aafd72cde05aa4b06b7523))
* **status-page:** persist enable_push_notifications through the API ([dd7d91c](https://github.com/yassinebenameur/probara/commit/dd7d91ceb555e0b1b7fdb2601c29bcb85b25e762))
* **status-page:** preserve public page state on live refresh ([fc1428b](https://github.com/yassinebenameur/probara/commit/fc1428bebf893bfdc109bf5d44868cd9128cdb86))
* **status-pages:** declutter and polish the status page form ([bcf9a99](https://github.com/yassinebenameur/probara/commit/bcf9a9913c61e21fecd25446a792378fccc21281))
* **status-page:** show theme and notification toggles in kiosk mode ([c976a50](https://github.com/yassinebenameur/probara/commit/c976a5060b8a77469fc5b9fbb6dee6188bd5b81a))
* **status-page:** sync uptime ranges and status copy ([51dfe57](https://github.com/yassinebenameur/probara/commit/51dfe5716dd07140fc1565c07e753110b32d8291))
* **status-page:** use rollups for 24h monitor aggregates ([b04c6ec](https://github.com/yassinebenameur/probara/commit/b04c6ec1e61c3a55efb2ea9e3f1bb5cbb6ea76d4))
* ui of http monitor ([a228afd](https://github.com/yassinebenameur/probara/commit/a228afda26475d15b93eebde87feb1995894225e))
* ui of http monitor form ([b1876fd](https://github.com/yassinebenameur/probara/commit/b1876fd8897a58c529224ce95ee8d4e058e7fc7d))
* ui of other monitors ([62d5a36](https://github.com/yassinebenameur/probara/commit/62d5a365308e2b09315b1fb03abef47d96edd6b7))
* **web:** add required notification fields to monitor-list test fixture ([5997f4b](https://github.com/yassinebenameur/probara/commit/5997f4b20b5777798cfc86f64dd9f85da1f2850c))
* **web:** align the top bar with the sidebar header ([a24d8a5](https://github.com/yassinebenameur/probara/commit/a24d8a5a26108cc06b7de7f8f67ad7cf1ca46c6e))
* **web:** align username/password inputs in database monitor forms ([dce8d7d](https://github.com/yassinebenameur/probara/commit/dce8d7d81b04cc3152b23fc0984e9b396ccf95db))
* **web:** centre row-action icons; stop search icons covering text ([6226a32](https://github.com/yassinebenameur/probara/commit/6226a32d87d9ef034229e2650746a485c6ef5734))
* **web:** chart Mongo cluster metrics over the selected range ([6d19f00](https://github.com/yassinebenameur/probara/commit/6d19f0050a7ab6ab4ec9ca90025dbdfb9a39451b))
* **web:** correct dashboard attention, open-incident badge, expired keys ([c6d1047](https://github.com/yassinebenameur/probara/commit/c6d1047bee4725626ef75f2fdd9babc5197aae51))
* **web:** email recipient field ate the separator, blocking a second address ([9657121](https://github.com/yassinebenameur/probara/commit/9657121c9a9be37e62eae7aee7a6c5c26045c081))
* **web:** honest worst-case detection hint; warn on bulk mute ([24abf40](https://github.com/yassinebenameur/probara/commit/24abf406a17aa3f7c9307e3dc0963c8f2e7ebbb3))
* **web:** proxy agent binary downloads ([47757db](https://github.com/yassinebenameur/probara/commit/47757db35baa16c013a4269b6076085bf6dfab99))
* **web:** reload page data when the tenant switcher changes tenant ([cfd7835](https://github.com/yassinebenameur/probara/commit/cfd7835570f2942b724c074e3c3ef8e2773a22b3))
* **web:** repair auth proxy and settings tag menu ([82b979a](https://github.com/yassinebenameur/probara/commit/82b979a895a2456aae400fc7240b3b61eedff953))
* **web:** replace removed btn-* classes with Button component in bulk policy UI ([44d6250](https://github.com/yassinebenameur/probara/commit/44d6250cc9cbb6a77a8b2f270226527b554b28b1))
* **web:** replay active alert toasts on connect ([1442bd5](https://github.com/yassinebenameur/probara/commit/1442bd52fa3f7a128423a1bbbc0bcf9c4fdf7213))
* **web:** resolve dashboard page.tsx merge conflicts ([e5e3d84](https://github.com/yassinebenameur/probara/commit/e5e3d84d8754d12f76513972e841b84d166a9a1a))
* **web:** resolve status page URLs from runtime origin and public_url ([4dea7c4](https://github.com/yassinebenameur/probara/commit/4dea7c44c13ef58f0c999bf4de18eba832cba1de))
* **website:** respect GitHub Pages base path ([d187cef](https://github.com/yassinebenameur/probara/commit/d187cef5eb436f7917fb5c80b154403c73331382))
* **web:** unify toasts, confirm dialogs, and fix clipped group actions menu ([06042a5](https://github.com/yassinebenameur/probara/commit/06042a5264f2f239086e03e3b3bbe9d16f16e353))
* **worker,alerter:** fail liveness once NATS connection is permanently closed ([7b795c2](https://github.com/yassinebenameur/probara/commit/7b795c2db1a6cc538ce5dd3c8252c8088ef6faab))
* **worker,alerter:** fail liveness once NATS connection is permanently closed ([25f5017](https://github.com/yassinebenameur/probara/commit/25f5017422758ba32446480ee80bc269f7b5f4ea))
* **worker:** make synthetic browser fill work on Chrome 149; bump CI actions to node24 ([32cde0c](https://github.com/yassinebenameur/probara/commit/32cde0c89901aa3ead463d136a84ebb453399cfd))
* **worker:** recover consumer loop after nats heartbeat loss ([8a3051f](https://github.com/yassinebenameur/probara/commit/8a3051f11ffd48273159ee1ce75c06924e96e41e))


### Features

* add portable monitor export and reimport support ([82e2ab2](https://github.com/yassinebenameur/probara/commit/82e2ab2b06a69afa008f04bd6940512dd1509c2c))
* add tenant data retention settings with 0-as-unlimited and daily cleanup ([9f93f11](https://github.com/yassinebenameur/probara/commit/9f93f111e91722512b74f29c6684fad69b861e76))
* **agent:** move host monitoring to OpenTelemetry and a generic metric store ([79dc315](https://github.com/yassinebenameur/probara/commit/79dc315d18de80cef44aa44ac79dd03e2b359d81))
* **agent:** supervise installs and mark stale agents down ([8b37b28](https://github.com/yassinebenameur/probara/commit/8b37b288fae1b0ce5b4baf6f80bcf2cc4d5ac5a6))
* **agent:** support remote service uninstall ([09a58cc](https://github.com/yassinebenameur/probara/commit/09a58ccb52c13d5201657ff02de77aa27db89846))
* **alerter:** auto-create incidents from lifecycle alerts (policy-less) ([e7fd5c6](https://github.com/yassinebenameur/probara/commit/e7fd5c6e0fb21f4e5cc0d67716e0c378a2642e3e))
* **alerter:** transition-driven alert lifecycle with per-channel escalation; retire window-based evaluator ([4c2d058](https://github.com/yassinebenameur/probara/commit/4c2d05872fce2546e624cb220170b1fac65e3cb8))
* **alerting:** dedicated tls_expiry alert kind for certificate expiry ([4fe1df8](https://github.com/yassinebenameur/probara/commit/4fe1df8f149787ff9667d42764b5635d41336df8))
* **alerting:** dependency-aware alerting — page root causes only ([fd8a8ff](https://github.com/yassinebenameur/probara/commit/fd8a8ffa46222386b3e61502b8025a8a870c6c5d))
* **alerting:** surface monitors whose alerts reach nobody ([1c7186c](https://github.com/yassinebenameur/probara/commit/1c7186c07f3468ed56d9d181bfa4971911b5fa32))
* **alerts:** introduce alert channel plugin system ([bf9a399](https://github.com/yassinebenameur/probara/commit/bf9a399198f905b22febb0aee44d3fd534ed1a5d))
* **alerts:** latency anomaly detection to catch degradation before hard-down ([f729aee](https://github.com/yassinebenameur/probara/commit/f729aeea8eea5dffdeab03b4d0980a9743a6d53b))
* **alerts:** per-group alert roll-up (per-monitor vs one group alert) ([0dd3c9c](https://github.com/yassinebenameur/probara/commit/0dd3c9ca05b22f32b6fa61a06863b9b1e7eab0fa))
* **api:** add core incident domain service ([2338223](https://github.com/yassinebenameur/probara/commit/2338223521aae4851213eabb4e4d8d3dee53b4ab))
* **api:** add incident linking and publication flows ([c26f18d](https://github.com/yassinebenameur/probara/commit/c26f18d0fae05496795c357712019d54b6c5248d))
* **api:** add POST /monitors/bulk/delete for soft bulk delete ([38fbdce](https://github.com/yassinebenameur/probara/commit/38fbdce9c8990ce5b997023098890af240fa0e2c))
* **api:** expose core incident endpoints ([d313bc0](https://github.com/yassinebenameur/probara/commit/d313bc009676ef7a6f55821c56db20badef3697a))
* **api:** include channel name/type in monitor notification channel assignments ([38d697d](https://github.com/yassinebenameur/probara/commit/38d697d6c47f01830455256075d0b254f39de7ca))
* **api:** monitor sensitivity + notification routing fields, bulk alerting endpoint ([21daa18](https://github.com/yassinebenameur/probara/commit/21daa18ac00ccc0454677b924f846c38c5e35a6f))
* **api:** retire alert-policy endpoints (410 Gone) ([ae197cd](https://github.com/yassinebenameur/probara/commit/ae197cd65a1b8b33acca37fce068a6a3f4825701))
* **api:** workspace notification settings endpoints ([1c2fe99](https://github.com/yassinebenameur/probara/commit/1c2fe99cddc5980112863212183fb5f856ba30a6))
* **auth:** enterprise auth — RBAC, audit log, scoped API keys & OIDC SSO ([0df57cf](https://github.com/yassinebenameur/probara/commit/0df57cf80b3ff6552b006ece5998a805eb2f8460))
* **auth:** map OIDC groups to roles with login-time sync and settings editor ([79281a0](https://github.com/yassinebenameur/probara/commit/79281a0cc80ed2b06c3560d4dea2d036ffd6d617))
* **config:** add monitor purge worker tunables ([432233f](https://github.com/yassinebenameur/probara/commit/432233f68b1f03e79c256801b3579614754abb4b))
* **dashboard:** add DashboardGroup model and extend summary response ([ec17950](https://github.com/yassinebenameur/probara/commit/ec1795032e2dcfad33f259b3481e026b72055537))
* **dashboard:** add hourly rollups for short ranges ([0c1b2bf](https://github.com/yassinebenameur/probara/commit/0c1b2bfa1c5593f44a86739dd5ce5084499a3e8a))
* **dashboard:** add loadExactRolling24hSummary helper ([bd6e9ee](https://github.com/yassinebenameur/probara/commit/bd6e9eed6a47e7c3e4061756b04f066fee7d7bc6))
* **dashboard:** add loadHourlyBucketSeries24h helper ([7103947](https://github.com/yassinebenameur/probara/commit/7103947a2792aa53e4a315c4199f2ec7f19ba340))
* **dashboard:** add tag-scoped stats filtering ([a5ac6ad](https://github.com/yassinebenameur/probara/commit/a5ac6adb07512453e6ef7189252ef9d2434baf11))
* **dashboard:** aggregate tag-based groups in summary response ([a5d7fc8](https://github.com/yassinebenameur/probara/commit/a5d7fc8fe10dadc5326c59aaa280aa32d6c05a47))
* **dashboard:** derive monitor health from persisted state machine (D1) ([8f786fc](https://github.com/yassinebenameur/probara/commit/8f786fc4ddde443697300cd516fca9fd2ee2d472))
* **dashboard:** expandable group rows with lazy sparkline ([5f42c4e](https://github.com/yassinebenameur/probara/commit/5f42c4e59018d9894d60390eafcaadfd9dab126f))
* **dashboard:** GET /dashboard/group-sparkline route ([afc3f13](https://github.com/yassinebenameur/probara/commit/afc3f13c8511afeb0a7fb7ebecb1a2c8f0751c6b))
* **dashboard:** per-group sparkline service method ([c1faf38](https://github.com/yassinebenameur/probara/commit/c1faf38a9ed45f3a8b887c8334bba5e5797ce6ff))
* **dashboard:** real service-groups panel with empty-state CTA ([5e5dc9b](https://github.com/yassinebenameur/probara/commit/5e5dc9b7ace3ce9992bd6973ba84ea3d37f10842))
* **dashboard:** split heavy overview loading ([3a3416c](https://github.com/yassinebenameur/probara/commit/3a3416cba08050e9324a3d10202be8bb99084e13))
* **dashboard:** surface failure reasons in needs-attention and what-changed panels ([cb46625](https://github.com/yassinebenameur/probara/commit/cb466257b00dbc434564e36641f97192a8252813))
* **db:** add dashboard_group_tags column to tenants ([f11ff95](https://github.com/yassinebenameur/probara/commit/f11ff95aeb82e9131a83c1f3b36881ea54f183c0))
* **db:** add monitor state machine and sensitivity columns ([d25ddcc](https://github.com/yassinebenameur/probara/commit/d25ddcc77464053bfd8cfaa06b789a762e92bdf7))
* **db:** add monitors.deleted_at and partial unique indexes for soft delete ([ff3df2e](https://github.com/yassinebenameur/probara/commit/ff3df2e0dce1e0d0f43b12babd80c41ff4d8196f))
* **db:** notification routing tables, workspace alert settings, policy backfill ([6649a73](https://github.com/yassinebenameur/probara/commit/6649a739e9225176f8355f11fcfd2a84fa972c6b))
* **dependencies:** AI-suggested dependencies from co-firing alerts, tags, groups ([a817e35](https://github.com/yassinebenameur/probara/commit/a817e353c9980cccabe3d61e0eafa4c08185d135))
* **email:** render alert email as branded HTML with a readable body ([53a6c5a](https://github.com/yassinebenameur/probara/commit/53a6c5ae728134072d2b1a7a81129824b8ffd1b2))
* filter soft-deleted monitors from every read and update site ([6dbb304](https://github.com/yassinebenameur/probara/commit/6dbb30404f709ea600c7183b3fe0df1a8d0e4c63))
* **helm:** extraEnv on every workload ([2d75045](https://github.com/yassinebenameur/probara/commit/2d7504552b958999367f787cbd47b264ebe88571))
* **helm:** source every secret from existing or external secrets ([87215dc](https://github.com/yassinebenameur/probara/commit/87215dca63d2ac331f011bc60ac69760b5a99ded))
* **host-metrics:** enterprise host metrics — richer collection, charts & threshold alerts ([1491809](https://github.com/yassinebenameur/probara/commit/1491809bd50f397b0b2931dc48a0faf8274b1e6a))
* **import:** migrate monitors from Uptime Kuma ([8a40dbe](https://github.com/yassinebenameur/probara/commit/8a40dbeb686bcc27cb6cf53ca3f8be128286de9a))
* **incidents:** add metadata and contextual creation ([6b7db23](https://github.com/yassinebenameur/probara/commit/6b7db2367c88fc06596b6eca6f57ee04a481a968))
* **incidents:** AI root cause analysis with per-tenant configurable LLM ([5488bd1](https://github.com/yassinebenameur/probara/commit/5488bd1fce330d921a4e1fe5614e56b66eb03e29))
* **incidents:** finish MVP implementation ([f4f7aa2](https://github.com/yassinebenameur/probara/commit/f4f7aa2043886a3f8b13241f68e908475c2e662b))
* init project ([d388e3c](https://github.com/yassinebenameur/probara/commit/d388e3c06998e9953133b19bcc8cf443ac15d472))
* **local:** improve status pages and local workflows ([643c514](https://github.com/yassinebenameur/probara/commit/643c5141e0bd15bed1e046d47ec92dee645b70a4))
* **locations:** private locations & multi-location checks ([2e5abd0](https://github.com/yassinebenameur/probara/commit/2e5abd0d8e95e4738b3e7780da816a3cf88c8e09))
* **maintenance:** select targets by group or tag, and select all at once ([1ba4e2b](https://github.com/yassinebenameur/probara/commit/1ba4e2bb2c46b42d672fe7597f52e522b04cad2f))
* **mesh:** inter-location connectivity mesh — probes, edge state, alerts & UI ([0703298](https://github.com/yassinebenameur/probara/commit/07032981b13eb4b5813d704fdc8b9ea6f538bcbe))
* **models:** add dashboard_group_tags to tenant settings ([d17cce8](https://github.com/yassinebenameur/probara/commit/d17cce88973462de445733b3fb27b83b207187d2))
* **models:** add DeletedAt to Monitor and select it in repo queries ([5ac7d6b](https://github.com/yassinebenameur/probara/commit/5ac7d6b6affb97293ceb5b9b8bf1a5ddf6fc4e87))
* **monitors:** add bulk alert policy request/response types ([9165e34](https://github.com/yassinebenameur/probara/commit/9165e3416cd8c2541da590f7832715f6fc737124))
* **monitors:** add BulkAttachAlertPolicy and BulkDetachAlertPolicy repo methods ([d0d790b](https://github.com/yassinebenameur/probara/commit/d0d790b27391bc27d4be3a8c9f2ee5b3f20c27ef))
* **monitors:** add BulkUpdateAlertPolicy HTTP handler ([28d1ee9](https://github.com/yassinebenameur/probara/commit/28d1ee9c26a8213da493adbb371798df55360cfa))
* **monitors:** add BulkUpdateAlertPolicy service method ([df7745d](https://github.com/yassinebenameur/probara/commit/df7745d121f3e7a6e81da5305615095e3c58b9ac))
* **monitors:** add clusterMonitor-backed cluster checks to the MongoDB monitor ([3c6b3c2](https://github.com/yassinebenameur/probara/commit/3c6b3c2c4b53e5eb00603fe3747663dc178b1cd9))
* **monitors:** add grpc monitor type with health-check execution, import support, UI integration, and unit tests ([cbd5f9a](https://github.com/yassinebenameur/probara/commit/cbd5f9a2418e6ff28526b9c1df5522d0f6e8d581))
* **monitors:** add mongod process-CPU cluster check ([69dc29a](https://github.com/yassinebenameur/probara/commit/69dc29ae4d368a00ed631c70e03fb98a08a8b1be))
* **monitors:** add monitor cloning and group management updates ([7d45165](https://github.com/yassinebenameur/probara/commit/7d45165f466112496a0630c7030cd7a7ce3d17cf))
* **monitors:** add Prometheus instant-query monitor type ([c817eb1](https://github.com/yassinebenameur/probara/commit/c817eb1f2eab34d655a7307f824d281bf5ad5fcd))
* **monitors:** add Redis, PostgreSQL, and MongoDB monitor types with encrypted config secrets ([93d5c0e](https://github.com/yassinebenameur/probara/commit/93d5c0e5093faa776673be672884c2e5c72db8ce))
* **monitors:** add tcp monitor type ([5b97fa9](https://github.com/yassinebenameur/probara/commit/5b97fa9a765f7f9869e6986613951c91bfd61055))
* **monitors:** add VerifyMonitorsBelongToTenant repository method ([574a52f](https://github.com/yassinebenameur/probara/commit/574a52f117dbbc247654e55763e0426369b1bd16))
* **monitors:** dependency graph with dependency-aware alert annotations ([d62032f](https://github.com/yassinebenameur/probara/commit/d62032f0e7eec36e35030a4bc3116036a2d40e02))
* **monitors:** exclude tombstoned monitors from repository reads ([2e7ce2c](https://github.com/yassinebenameur/probara/commit/2e7ce2cbaa176edd98afcd81d50d8689659d734d))
* **monitors:** group-aware filtering and polished group UI ([a7164e9](https://github.com/yassinebenameur/probara/commit/a7164e96223a9f41b7c3cc6e2e3d44481d28b362))
* **monitors:** maintenance windows with alert suppression and snooze ([8d8f341](https://github.com/yassinebenameur/probara/commit/8d8f341108d896e5fd6f01f5a4aeabc21d91f8ca))
* **monitors:** maintenance windows with alert suppression and snooze ([bbcce52](https://github.com/yassinebenameur/probara/commit/bbcce527ebd63671cb1b30b4988f5881d89eb770))
* **monitors:** MySQL, WebSocket & DNS custom-resolver types — Tier 1 complete ([f2b928b](https://github.com/yassinebenameur/probara/commit/f2b928beddda2c453973fdd079346d92645eca8c))
* **monitors:** RabbitMQ type, test-connection flow, richer DB assertions, key rotation ([9f6b9e2](https://github.com/yassinebenameur/probara/commit/9f6b9e200af7d175e87f5cc610d4e0afa1d670be))
* **monitors:** record a state timeline and stop inventing uptime ([d7b9cf1](https://github.com/yassinebenameur/probara/commit/d7b9cf18a50f831b66da6836cd9e32cc534b7e84))
* **monitors:** redesign monitor form type picker and unify form layout ([9e4ec5a](https://github.com/yassinebenameur/probara/commit/9e4ec5a456574a869c125bd778384d9558ab0e28))
* **monitors:** show JSON response values on the monitors list ([f2e299d](https://github.com/yassinebenameur/probara/commit/f2e299d07ba96417847371f63c2018a2c70c5188))
* **monitors:** soft delete via tombstone + BulkSoftDelete/HardDelete repo methods ([95320ef](https://github.com/yassinebenameur/probara/commit/95320efebcf6c498ac7f018822c0096fd712dde9))
* **monitorstate:** pure state-machine transition logic ([43809c0](https://github.com/yassinebenameur/probara/commit/43809c001fbf62b6a28c2ecf87c8e1a87a3aeba5))
* **monitors:** wire POST /v1/monitors/bulk/alert-policy route ([14659e2](https://github.com/yassinebenameur/probara/commit/14659e2c5648e8e03a5630844c9f37097464ed1b))
* **notifications:** harden the channel plugin system and add paging, Telegram and SMS channels ([309f20a](https://github.com/yassinebenameur/probara/commit/309f20ae6ddafc131d60cac856beb3aad582d72e))
* promote dev to main ([55747fc](https://github.com/yassinebenameur/probara/commit/55747fcaec259a4ad95d0bfa6b38e33c49e9141e))
* rollups ([3498b53](https://github.com/yassinebenameur/probara/commit/3498b535702782987bc2afe4e02c1e44498c3a96))
* **scheduler:** add async purger for soft-deleted monitors ([57b7218](https://github.com/yassinebenameur/probara/commit/57b72183c6634866179528d6fff3821a1e72e986))
* **scheduler:** add per-batch debug logs to monitor purger ([9abf6ca](https://github.com/yassinebenameur/probara/commit/9abf6ca33d34f1733393896e5a810b43f59efba7))
* **scheduler:** fast-recheck suspect monitors at min(interval, 20s) ([5205c86](https://github.com/yassinebenameur/probara/commit/5205c86d512759877747a45a4d7d019ba1eec9a2))
* **scheduler:** skip poisoned rollup rows; track error_checks in rollups ([7cf7bf2](https://github.com/yassinebenameur/probara/commit/7cf7bf26a62af06a398971aeff1e42da2e7c3417))
* **scheduler:** skip soft-deleted monitors when fetching due work ([5116c62](https://github.com/yassinebenameur/probara/commit/5116c6252a4572c03ef0a2ae10df87d86e86e5be))
* **scripts:** idempotent hourly/daily rollup backfill ([9bc79d5](https://github.com/yassinebenameur/probara/commit/9bc79d5a8ec9ad5cd047eaafadbe44f4d51664b0))
* **settings:** add dashboard groups tag picker ([b079324](https://github.com/yassinebenameur/probara/commit/b0793247ad35738552e7d2f0007e8d474287ffd1))
* **sip:** REGISTER auth probes, digest authentication, TLS transport ([0a77efe](https://github.com/yassinebenameur/probara/commit/0a77efe9ec02999a3097428b022282378776d782))
* **sla:** SLA definitions, calendar reports, error budgets and exports ([8ed3bda](https://github.com/yassinebenameur/probara/commit/8ed3bdae3e71a2cf41fa2f2f1f2d9067f1122ae9))
* status page custom templates + dependencies graph redesign ([800bfbc](https://github.com/yassinebenameur/probara/commit/800bfbcfb242bf1eb64022c98fdbb57ec79806f0))
* **status-page:** add theme settings and dedicated renderer ([dd40e32](https://github.com/yassinebenameur/probara/commit/dd40e32e9cdc10e9a83eed02966543db1f31c152))
* **status-page:** auto-fit kiosk tile density so every monitor stays on screen ([1b22b54](https://github.com/yassinebenameur/probara/commit/1b22b54233a24b70759e55cfc88fc12df87e917b)), closes [#kioskGrid](https://github.com/yassinebenameur/probara/issues/kioskGrid)
* **status-page:** bell icon for the notification toggle, and fix unsubscribe ([0605dec](https://github.com/yassinebenameur/probara/commit/0605dec3f1898a55e2ee6b61408182f1b356cfe8))
* **status-page:** certificate-expires-soon note on public components ([d5e9be5](https://github.com/yassinebenameur/probara/commit/d5e9be59718bee7f06d8869326ef55bc5f552b53))
* **status-page:** collapsible sections, denser compact/kiosk modes, refined bars ([de607f8](https://github.com/yassinebenameur/probara/commit/de607f86cb8453a461848a12320875973e03207c))
* **status-page:** harden SSE subscriber (no-client exit, slug cache, per-slug throttle) ([75f5572](https://github.com/yassinebenameur/probara/commit/75f5572a8cd2f9e601ac6f54e959a91129a1a479))
* **status-page:** icon theme toggle and header control polish ([d914dfe](https://github.com/yassinebenameur/probara/commit/d914dfe66a27d78eab43dd402491d59937c70e88))
* **status-page:** per-slug render cache with ETag/304 and SSE invalidation ([e1e772f](https://github.com/yassinebenameur/probara/commit/e1e772ff8ad02bbd68972f8d896dab5c7e4588b7))
* **status-page:** push subscribe endpoints and service worker route ([8821c63](https://github.com/yassinebenameur/probara/commit/8821c63f7d27f69018c2804b6c4d42a1542d6966))
* **status-page:** reconcile state transitions into browser push deliveries ([52036b0](https://github.com/yassinebenameur/probara/commit/52036b038f0c75e34cf03518b3980f710bff506a))
* **status-page:** redesign editor — live preview, section-first layout, inline monitor picker ([09c9598](https://github.com/yassinebenameur/probara/commit/09c95982f72c3d0c5c34d808acad16d1f9a764b9))
* **status-page:** redesign public page rendering and scoped live updates ([470d883](https://github.com/yassinebenameur/probara/commit/470d8839cbab061ce7ed3bec89f1a313e73f330f))
* **status-page:** reveal full kiosk tile text in hover popover ([285c8e4](https://github.com/yassinebenameur/probara/commit/285c8e4edbef543afc587284b70b68236b91d841))
* **status-page:** schema and settings for visitor push notifications ([b81fbb1](https://github.com/yassinebenameur/probara/commit/b81fbb1a14598b1248af552e39ac374a58e50838))
* **status-page:** support incident-only refresh events ([2a2e4cb](https://github.com/yassinebenameur/probara/commit/2a2e4cb179bd680d6bf1ecb3adf0e9c0c14ba90c))
* **status-page:** visitor notification toggle, VAPID config, and deployment ([fcb0f91](https://github.com/yassinebenameur/probara/commit/fcb0f9155cbb34a504bd9a8ead03a1caaa446d86))
* **status,dashboard:** derive monitor status from persisted state machine ([70391cf](https://github.com/yassinebenameur/probara/commit/70391cfe20752ef1bcb1ca152f4032424d56db59))
* **tenants:** accept dashboard_group_tags in PATCH settings ([8ac1bdc](https://github.com/yassinebenameur/probara/commit/8ac1bdc66c5b91147115bba25a10e73e448e5421))
* **tenants:** persist dashboard_group_tags ([f810400](https://github.com/yassinebenameur/probara/commit/f81040084febb078299c9ee8cf3b456131569186))
* **validation:** validate dashboard_group_tags shape ([8fb0963](https://github.com/yassinebenameur/probara/commit/8fb09637ec26bf6ae75eea463e3692cbafe2c597))
* **web,status-page:** unify the platform logo on the trace mark ([f165f79](https://github.com/yassinebenameur/probara/commit/f165f7941ef5c723a4a6e1c090d2519caa2bf61a))
* **web/ui:** add FilterChip with selected state and count slot ([e9d4950](https://github.com/yassinebenameur/probara/commit/e9d4950259c564e4cd35e58c1ebdcaf0683e4573))
* **web/ui:** add FormActions bar with optional sticky mode ([f5268ec](https://github.com/yassinebenameur/probara/commit/f5268ecbac62b23a9813f8b94dee38a41957cf5d))
* **web/ui:** add FormCard outer container ([556e80d](https://github.com/yassinebenameur/probara/commit/556e80db9c37eb5b2be8f4a79857f1630838aba3))
* **web/ui:** add FormSection container ([8574629](https://github.com/yassinebenameur/probara/commit/8574629aa12e6f37c9acfb3d13f296c3d633eb3b))
* **web/ui:** add infoTip prop to FormField ([b72fbce](https://github.com/yassinebenameur/probara/commit/b72fbced09d021415a52469fb9d497f47525b2d4))
* **web/ui:** add PageHeader and Breadcrumb primitives ([820e427](https://github.com/yassinebenameur/probara/commit/820e42797e698ede7296502e5783497ae70a6249))
* **web/ui:** add Pill primitive with success/danger/warning/info/neutral/tag tones ([10e9f7a](https://github.com/yassinebenameur/probara/commit/10e9f7a7e175e67a71b1e4b3273c5851d1ebe909))
* **web/ui:** extract InfoTip popover primitive with portal positioning ([a439899](https://github.com/yassinebenameur/probara/commit/a4398990de7f714b97da78bd9b00997f13b881d1))
* **web/ui:** rewrite Button as ghost/accent/danger/subtle primitive ([58411af](https://github.com/yassinebenameur/probara/commit/58411af9737ab138734da42882371d545c6d0039))
* **web/ui:** rewrite EmptyState for dark theme ([275f155](https://github.com/yassinebenameur/probara/commit/275f155dd88f89c73770616222d5b207d28ecb1e))
* **web:** add BulkAttachPolicyDialog component ([27a35b1](https://github.com/yassinebenameur/probara/commit/27a35b144252df5f757a0e102a96e26e309282d3))
* **web:** add bulkUpdateMonitorAlertPolicy API client ([f869a4b](https://github.com/yassinebenameur/probara/commit/f869a4b9bb452eadfca6f065ee77407aea90bf56))
* **web:** add global alert SSE toasts ([2b9d5f5](https://github.com/yassinebenameur/probara/commit/2b9d5f5852473bd9f69a2e68827342ff1beba646))
* **web:** add operator UI favicon ([7c81e5e](https://github.com/yassinebenameur/probara/commit/7c81e5e6b258e23c5d6be0d4f4854b1e8e4e42cd)), closes [#ff5a24](https://github.com/yassinebenameur/probara/issues/ff5a24) [#140a05](https://github.com/yassinebenameur/probara/issues/140a05)
* **web:** alerting section in group/agent/push/sip/grpc monitor forms ([1e04609](https://github.com/yassinebenameur/probara/commit/1e046098d4c86da01b05d2616dc2e0e8797229df))
* **web:** bulk edit alerting; remove alert-policies UI ([6d299c8](https://github.com/yassinebenameur/probara/commit/6d299c85d8a12fdb4f68c5b5026517dcb82c0d22))
* **web:** chart MongoDB cluster metrics on the monitor detail page ([6ce5bd8](https://github.com/yassinebenameur/probara/commit/6ce5bd8d84a1676ed3eb82d6c0f0f471fcf4551b))
* **web:** dashboard group types + sparkline api client ([d3bf88c](https://github.com/yassinebenameur/probara/commit/d3bf88c73e8a5dcd568bc90d1bdfe0cb4b0d7eda))
* **web:** enable visitor browser notifications from the status page form ([35d498c](https://github.com/yassinebenameur/probara/commit/35d498c98c980612e2cbfe1556de56271fe1b5b2))
* **web:** flight-recorder redesign with light/dark theming ([33ca0b7](https://github.com/yassinebenameur/probara/commit/33ca0b76316d6180f9869ee11714c11b80b8e3cd))
* **web:** fold Mesh into Locations, trim sidebar chrome ([4e5b447](https://github.com/yassinebenameur/probara/commit/4e5b447073237c6a775d5ce0746ecbe2240b43ae))
* **web:** make monitor targets copyable from the list and detail views ([bcfca74](https://github.com/yassinebenameur/probara/commit/bcfca746ae731abb2fb3f0509a1d7e580997b67f))
* **web:** monitor form alerting section (sensitivity + notifications picker) ([dad9a90](https://github.com/yassinebenameur/probara/commit/dad9a9082c9ee1d94480fec9d102ad91916e4d70))
* **webpush:** RFC 8291 message encryption and RFC 8292 VAPID auth ([a992438](https://github.com/yassinebenameur/probara/commit/a99243842331b4a2d926cc1b3fc4fd3f98635d1d))
* **web:** settings tabs; alert channels move into Settings ([b59a0b8](https://github.com/yassinebenameur/probara/commit/b59a0b8b565a5f4581b328dd9359a01e693fbde7)), closes [settings#dashboard-groups](https://github.com/settings/issues/dashboard-groups)
* **web:** shared ChannelPicker with inline create and escalation delays ([9caecdd](https://github.com/yassinebenameur/probara/commit/9caecddedbee9a00b6b4ae9efc664939804558dd))
* **website:** add landing page and documentation ([438fa46](https://github.com/yassinebenameur/probara/commit/438fa46e4a2bcef4c69139543f8c5cc9fdd34318))
* **website:** docs cross-links, inline link rendering & landing polish ([f4c8ce5](https://github.com/yassinebenameur/probara/commit/f4c8ce5238e4f692fd3e2a989d808e926749a7ac))
* **web:** types and client for notification settings and bulk alerting ([9115be4](https://github.com/yassinebenameur/probara/commit/9115be41c9e96f2620876193e6ac6e94feea203e))
* **web:** wire BulkAttachPolicyDialog into alert policy detail page ([a00acf1](https://github.com/yassinebenameur/probara/commit/a00acf1d31237446e6bd3010dd159d300105c863))
* **web:** wire BulkAttachPolicyDialog into monitors list page ([9bbb1a0](https://github.com/yassinebenameur/probara/commit/9bbb1a0e2a35db96ff8e09e17c241d6c7d9a6816))
* **web:** workspace notifications settings panel and day-0 nudge banner ([f0cdfcc](https://github.com/yassinebenameur/probara/commit/f0cdfcc6777748f86bc1ef32d95f66c127e4f8c5))
* **worker:** advance monitor state machine transactionally with each result ([de9db93](https://github.com/yassinebenameur/probara/commit/de9db93c9d7f264fdd42c7dc387bacae8d883ab3))
* **worker:** publish status updates only on monitor state transitions ([319f5aa](https://github.com/yassinebenameur/probara/commit/319f5aaa09ceb5c2145708b127e97ae4c5ad2be7))


### Performance Improvements

* **dashboard,status-page:** pass window bounds as parameters so check_results scans use indexes ([48448dc](https://github.com/yassinebenameur/probara/commit/48448dc916daf7216772e7d1ae159868fdd3cb6a))
* **dashboard:** bound 24h hourly raw scan ([b45905c](https://github.com/yassinebenameur/probara/commit/b45905c56ec37bd746a60166eb2e36e77179f0eb))
* **dashboard:** bound recent-failures next-success probe to emitted rows ([05baf10](https://github.com/yassinebenameur/probara/commit/05baf102d3517adaf8b5bcf434b19ef7100c53f2))
* **dashboard:** derive Activity24h from hour-aligned bucket helper ([2e70219](https://github.com/yassinebenameur/probara/commit/2e702194d44d7ab88cd43e87ef2ec330ae926466))
* **dashboard:** fix slow 24h summary via edge-range scan and dedupe ([d2004dd](https://github.com/yassinebenameur/probara/commit/d2004dd7b81a579b6796d46fdc31d3f5dccf0ba5))
* **dashboard:** share rolling-24h aggregates via short-TTL cache ([bfd0b43](https://github.com/yassinebenameur/probara/commit/bfd0b43da5a804fac4c271c008900a72fea927c6))
* **dashboard:** switch 24h problem monitors to exact-rolling helper ([bf21ba4](https://github.com/yassinebenameur/probara/commit/bf21ba4360a21c2066769e5b110fda210cd90080))
* **dashboard:** use exact-rolling hourly rollup helper for 24h stats ([6ae25ce](https://github.com/yassinebenameur/probara/commit/6ae25ce306b4c3cdf5fda9dfdc57901dc92e7382))
* **dashboard:** use exact-rolling summary for 24h groups ([7c970a8](https://github.com/yassinebenameur/probara/commit/7c970a80e3533bf69550e11382d09abde41e52bd))
* **dashboard:** use hour-aligned bucket helper for 24h trend ([d7cd8cb](https://github.com/yassinebenameur/probara/commit/d7cd8cb19f4695bbb694e6c5150373b44776f429))
* **dashboard:** use hour-aligned helper for 24h group sparkline ([fd43267](https://github.com/yassinebenameur/probara/commit/fd432674ad8081a27cab035153893953533e2d93))
* **scripts:** index-friendly bounds + retention warning for rollup backfill ([e472da3](https://github.com/yassinebenameur/probara/commit/e472da3db84a29a1aaa0541805d5ece1f2cceef2))
* **status-page:** batch monitor loading to fix N+1 query timeout ([71a21f3](https://github.com/yassinebenameur/probara/commit/71a21f30052da4a669c86bcb40196f2fa33460f6))
* **status-page:** bound global 24h hourly scan ([76d8344](https://github.com/yassinebenameur/probara/commit/76d8344a798425635463f453fa30b6a7f4e5ab4d))
* **status-page:** parse template once; batch incident queries ([a257432](https://github.com/yassinebenameur/probara/commit/a257432fb0d269949ec8f86d594f9d014eba87bc))

# [1.0.0-alpha.77](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.76...v1.0.0-alpha.77) (2026-10-10)


### Features

* **web:** consolidate the instrument-glass UI onto shared controls and helpers ([691b157](https://github.com/yassinebenameur/probara/commit/691b1577e0aafdc56f5b30066a6b995c5fd9c9a0))
* **web:** instrument-glass visual overhaul of the operator UI ([5d60fd8](https://github.com/yassinebenameur/probara/commit/5d60fd8f8a93f2d15989579c2c77bbf4559dea5a))

# [1.0.0-alpha.76](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.75...v1.0.0-alpha.76) (2026-09-29)


### Bug Fixes

* **web:** chart Mongo cluster metrics over the selected range ([6d19f00](https://github.com/yassinebenameur/probara/commit/6d19f0050a7ab6ab4ec9ca90025dbdfb9a39451b))


### Features

* **monitors:** show JSON response values on the monitors list ([f2e299d](https://github.com/yassinebenameur/probara/commit/f2e299d07ba96417847371f63c2018a2c70c5188))
* **sla:** SLA definitions, calendar reports, error budgets and exports ([8ed3bda](https://github.com/yassinebenameur/probara/commit/8ed3bdae3e71a2cf41fa2f2f1f2d9067f1122ae9))

# [1.0.0-alpha.75](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.74...v1.0.0-alpha.75) (2026-09-29)


### Bug Fixes

* **scheduler:** keep raw check_results 30 days, serve longer ranges from rollups ([663b275](https://github.com/yassinebenameur/probara/commit/663b27521fe4fa9dbbea842d04460095180ba619))

# [1.0.0-alpha.74](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.73...v1.0.0-alpha.74) (2026-09-28)


### Bug Fixes

* **api:** alert list time window keeps alerts that are still open ([2c34e01](https://github.com/yassinebenameur/probara/commit/2c34e01a5ea675a17f0d9478192f6490e603958d))
* **notifications:** close four delivery bugs found in review ([e9a6e8b](https://github.com/yassinebenameur/probara/commit/e9a6e8ba2a55d7fe1bfb6eb3b113ba11e379fa0a))
* **notifications:** order acks after late triggers, unblock alerter claims ([e46b7c1](https://github.com/yassinebenameur/probara/commit/e46b7c15f1001321de0e64bdc36d70f114e00e86))
* **notifications:** serialize paging delivery with resolution; keep SMS Retry-After ([96605e9](https://github.com/yassinebenameur/probara/commit/96605e97efea50afb8d6decebd33416181583105))
* **web:** align the top bar with the sidebar header ([a24d8a5](https://github.com/yassinebenameur/probara/commit/a24d8a5a26108cc06b7de7f8f67ad7cf1ca46c6e))
* **web:** centre row-action icons; stop search icons covering text ([6226a32](https://github.com/yassinebenameur/probara/commit/6226a32d87d9ef034229e2650746a485c6ef5734))
* **web:** correct dashboard attention, open-incident badge, expired keys ([c6d1047](https://github.com/yassinebenameur/probara/commit/c6d1047bee4725626ef75f2fdd9babc5197aae51))


### Features

* **notifications:** harden the channel plugin system and add paging, Telegram and SMS channels ([309f20a](https://github.com/yassinebenameur/probara/commit/309f20ae6ddafc131d60cac856beb3aad582d72e))
* **web:** fold Mesh into Locations, trim sidebar chrome ([4e5b447](https://github.com/yassinebenameur/probara/commit/4e5b447073237c6a775d5ce0746ecbe2240b43ae))
* **web:** settings tabs; alert channels move into Settings ([b59a0b8](https://github.com/yassinebenameur/probara/commit/b59a0b8b565a5f4581b328dd9359a01e693fbde7)), closes [settings#dashboard-groups](https://github.com/settings/issues/dashboard-groups)

# [1.0.0-alpha.73](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.72...v1.0.0-alpha.73) (2026-09-06)


### Bug Fixes

* **monitorstate:** keep the state timeline monotonic when a writer loses the lock race ([195c2dc](https://github.com/yassinebenameur/probara/commit/195c2dcf496feb69228b98fe13c812673f746996))


### Features

* **monitors:** add Prometheus instant-query monitor type ([c817eb1](https://github.com/yassinebenameur/probara/commit/c817eb1f2eab34d655a7307f824d281bf5ad5fcd))

# [1.0.0-alpha.72](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.71...v1.0.0-alpha.72) (2026-09-04)


### Bug Fixes

* **alerter:** claim each notification atomically so replicas never send it twice ([5e8d578](https://github.com/yassinebenameur/probara/commit/5e8d578dd8b2254e9cda5b81dda1c2c6ac1ced5e))
* **alerter:** resolve orphaned latency-anomaly alerts when no tenant has anomaly enabled ([d342318](https://github.com/yassinebenameur/probara/commit/d342318dcab20015a71951070268156f1cfe83b6))
* **platform:** secure credentials and improve monitoring reliability ([08ae85f](https://github.com/yassinebenameur/probara/commit/08ae85f21ae6b2764dbb6e03856a448dd82bd168))


### Features

* **alerting:** dependency-aware alerting — page root causes only ([fd8a8ff](https://github.com/yassinebenameur/probara/commit/fd8a8ffa46222386b3e61502b8025a8a870c6c5d))

# [1.0.0-alpha.71](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.70...v1.0.0-alpha.71) (2026-08-28)


### Bug Fixes

* **secrets:** keep stored monitor secrets when an update omits the field ([5e85e41](https://github.com/yassinebenameur/probara/commit/5e85e4176d20052c9b78748ed8087e26a2752341))
* **status-page:** align monitor titles with fixed-width type badges ([04e352e](https://github.com/yassinebenameur/probara/commit/04e352eaa09a3072248dcade61c9a145107f5738))
* **status-page:** make the visitor notification toggle actually work ([7107990](https://github.com/yassinebenameur/probara/commit/71079905148253d605aafd72cde05aa4b06b7523))
* **status-page:** persist enable_push_notifications through the API ([dd7d91c](https://github.com/yassinebenameur/probara/commit/dd7d91ceb555e0b1b7fdb2601c29bcb85b25e762))
* **status-page:** show theme and notification toggles in kiosk mode ([c976a50](https://github.com/yassinebenameur/probara/commit/c976a5060b8a77469fc5b9fbb6dee6188bd5b81a))


### Features

* **status-page:** auto-fit kiosk tile density so every monitor stays on screen ([1b22b54](https://github.com/yassinebenameur/probara/commit/1b22b54233a24b70759e55cfc88fc12df87e917b)), closes [#kioskGrid](https://github.com/yassinebenameur/probara/issues/kioskGrid)
* **status-page:** bell icon for the notification toggle, and fix unsubscribe ([0605dec](https://github.com/yassinebenameur/probara/commit/0605dec3f1898a55e2ee6b61408182f1b356cfe8))
* **status-page:** icon theme toggle and header control polish ([d914dfe](https://github.com/yassinebenameur/probara/commit/d914dfe66a27d78eab43dd402491d59937c70e88))
* **status-page:** push subscribe endpoints and service worker route ([8821c63](https://github.com/yassinebenameur/probara/commit/8821c63f7d27f69018c2804b6c4d42a1542d6966))
* **status-page:** reconcile state transitions into browser push deliveries ([52036b0](https://github.com/yassinebenameur/probara/commit/52036b038f0c75e34cf03518b3980f710bff506a))
* **status-page:** schema and settings for visitor push notifications ([b81fbb1](https://github.com/yassinebenameur/probara/commit/b81fbb1a14598b1248af552e39ac374a58e50838))
* **status-page:** visitor notification toggle, VAPID config, and deployment ([fcb0f91](https://github.com/yassinebenameur/probara/commit/fcb0f9155cbb34a504bd9a8ead03a1caaa446d86))
* **web:** enable visitor browser notifications from the status page form ([35d498c](https://github.com/yassinebenameur/probara/commit/35d498c98c980612e2cbfe1556de56271fe1b5b2))
* **webpush:** RFC 8291 message encryption and RFC 8292 VAPID auth ([a992438](https://github.com/yassinebenameur/probara/commit/a99243842331b4a2d926cc1b3fc4fd3f98635d1d))

# [1.0.0-alpha.70](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.69...v1.0.0-alpha.70) (2026-08-25)


### Features

* **alerting:** surface monitors whose alerts reach nobody ([1c7186c](https://github.com/yassinebenameur/probara/commit/1c7186c07f3468ed56d9d181bfa4971911b5fa32))
* **dashboard:** surface failure reasons in needs-attention and what-changed panels ([cb46625](https://github.com/yassinebenameur/probara/commit/cb466257b00dbc434564e36641f97192a8252813))
* **import:** migrate monitors from Uptime Kuma ([8a40dbe](https://github.com/yassinebenameur/probara/commit/8a40dbeb686bcc27cb6cf53ca3f8be128286de9a))

# [1.0.0-alpha.69](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.68...v1.0.0-alpha.69) (2026-08-20)


### Bug Fixes

* **api:** ship collector binaries in the api image and unbreak CI ([e71c473](https://github.com/yassinebenameur/probara/commit/e71c473c89bd85ec02c3614fc87d331a55747267))

# [1.0.0-alpha.68](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.67...v1.0.0-alpha.68) (2026-08-20)


### Bug Fixes

* **api:** stop rejecting agent monitor edits on timeout validation ([d6d663a](https://github.com/yassinebenameur/probara/commit/d6d663aa1500f6da98ceb2f0e4fcbf49103ddf91))
* **scheduler:** replace the rollup cursor with a dirty-bucket ledger ([1dbc041](https://github.com/yassinebenameur/probara/commit/1dbc0413509384d5ee6752b8e9c6cd666b3d9628)), closes [hi#water](https://github.com/hi/issues/water)
* **scheduler:** retry result ingest indefinitely instead of dropping ([c15711e](https://github.com/yassinebenameur/probara/commit/c15711eb6e685c496e48b5a9568c295bed03553b))


### Features

* **agent:** move host monitoring to OpenTelemetry and a generic metric store ([79dc315](https://github.com/yassinebenameur/probara/commit/79dc315d18de80cef44aa44ac79dd03e2b359d81))
* **monitors:** add clusterMonitor-backed cluster checks to the MongoDB monitor ([3c6b3c2](https://github.com/yassinebenameur/probara/commit/3c6b3c2c4b53e5eb00603fe3747663dc178b1cd9))
* **monitors:** add mongod process-CPU cluster check ([69dc29a](https://github.com/yassinebenameur/probara/commit/69dc29ae4d368a00ed631c70e03fb98a08a8b1be))
* **monitors:** record a state timeline and stop inventing uptime ([d7b9cf1](https://github.com/yassinebenameur/probara/commit/d7b9cf18a50f831b66da6836cd9e32cc534b7e84))
* **web:** chart MongoDB cluster metrics on the monitor detail page ([6ce5bd8](https://github.com/yassinebenameur/probara/commit/6ce5bd8d84a1676ed3eb82d6c0f0f471fcf4551b))

# [1.0.0-alpha.67](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.66...v1.0.0-alpha.67) (2026-08-11)


### Bug Fixes

* **website:** respect GitHub Pages base path ([d187cef](https://github.com/yassinebenameur/probara/commit/d187cef5eb436f7917fb5c80b154403c73331382))

# [1.0.0-alpha.66](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.65...v1.0.0-alpha.66) (2026-08-11)


### Features

* **helm:** source every secret from existing or external secrets ([87215dc](https://github.com/yassinebenameur/probara/commit/87215dca63d2ac331f011bc60ac69760b5a99ded))

# [1.0.0-alpha.65](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.64...v1.0.0-alpha.65) (2026-08-10)


### Bug Fixes

* **deps:** remediate runtime vulnerabilities ([e1b4ef0](https://github.com/yassinebenameur/probara/commit/e1b4ef03ea2cd3c1a5807a582856d083c07eb806))

# [1.0.0-alpha.64](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.63...v1.0.0-alpha.64) (2026-08-10)


### Features

* **auth:** map OIDC groups to roles with login-time sync and settings editor ([79281a0](https://github.com/yassinebenameur/probara/commit/79281a0cc80ed2b06c3560d4dea2d036ffd6d617))

# [1.0.0-alpha.63](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.62...v1.0.0-alpha.63) (2026-08-05)


### Features

* **email:** render alert email as branded HTML with a readable body ([3446c5c](https://github.com/yassinebenameur/probara/commit/3446c5ca02fb345cd6dd9f6863ffbae3600e00d0))

# [1.0.0-alpha.62](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.61...v1.0.0-alpha.62) (2026-08-04)


### Features

* **maintenance:** select targets by group or tag, and select all at once ([800c685](https://github.com/yassinebenameur/probara/commit/800c685c29405c808b6422b8d3b8b24bda69e92f))
* **web,status-page:** unify the platform logo on the trace mark ([38187ff](https://github.com/yassinebenameur/probara/commit/38187ff0d7a4255aed0c8e7e521a4ab40a9dd037))
* **web:** add operator UI favicon ([15c529c](https://github.com/yassinebenameur/probara/commit/15c529cc77787a3bd97af0448da7cfb108b08fa2)), closes [#ff5a24](https://github.com/yassinebenameur/probara/issues/ff5a24) [#140a05](https://github.com/yassinebenameur/probara/issues/140a05)
* **web:** make monitor targets copyable from the list and detail views ([d54b667](https://github.com/yassinebenameur/probara/commit/d54b66704b35313a473402ea3e1832b7666d537a))

# [1.0.0-alpha.61](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.60...v1.0.0-alpha.61) (2026-08-04)


### Bug Fixes

* **api:** wire SMTP mailer so email channel tests stop failing ([9616380](https://github.com/yassinebenameur/probara/commit/96163802297fa3a91490ac9548a7205419420728))
* **web:** email recipient field ate the separator, blocking a second address ([d5f5b5f](https://github.com/yassinebenameur/probara/commit/d5f5b5f10ef58ba930b0ec4a55c477348f8309f5))

# [1.0.0-alpha.60](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.59...v1.0.0-alpha.60) (2026-08-03)


### Bug Fixes

* **alerter:** tls_expiry cert query timed out and blocked all alert dispatch ([ff8098d](https://github.com/yassinebenameur/probara/commit/ff8098d6e85a72c0eca234b9098be3d36a9e3b84))

# [1.0.0-alpha.59](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.58...v1.0.0-alpha.59) (2026-08-03)


### Bug Fixes

* **auth:** stop concurrent refreshes from killing the session ([2590f58](https://github.com/yassinebenameur/probara/commit/2590f58a61cf6b432563c1c260f6cab147a39871))
* **web:** reload page data when the tenant switcher changes tenant ([d4ff745](https://github.com/yassinebenameur/probara/commit/d4ff7459d065c237c81108680d0253966c1d49c4))


### Features

* **alerting:** dedicated tls_expiry alert kind for certificate expiry ([b93e148](https://github.com/yassinebenameur/probara/commit/b93e148f16023b1d4a88e5b01e8e1e0f1c4934a1))
* **status-page:** certificate-expires-soon note on public components ([219e070](https://github.com/yassinebenameur/probara/commit/219e0704dc3754fb3110cbc474032526f98c2fb6))

# [1.0.0-alpha.58](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.57...v1.0.0-alpha.58) (2026-07-24)


### Bug Fixes

* **deploy:** align run-now check-job subject and wire scheduler secrets key ([faf844a](https://github.com/yassinebenameur/probara/commit/faf844aa96ace47bbe1e5316ea660b0006ed4867))
* **import:** accept all registered monitor types in portable import ([d5db8f8](https://github.com/yassinebenameur/probara/commit/d5db8f83aef46b8db76d7276f46ba72839f552e4))


### Features

* **helm:** extraEnv on every workload ([5dac1fc](https://github.com/yassinebenameur/probara/commit/5dac1fc8bf0c1746871c49baee985205774aaf5b))
* **sip:** REGISTER auth probes, digest authentication, TLS transport ([b83c57a](https://github.com/yassinebenameur/probara/commit/b83c57aff21612d76c5748226cf0a6b64cf4ac88))
* **web:** flight-recorder redesign with light/dark theming ([8956847](https://github.com/yassinebenameur/probara/commit/8956847122f26587fe67f4131919f060df24b3bc))
* **website:** add landing page and documentation ([b8c6146](https://github.com/yassinebenameur/probara/commit/b8c6146d23001e645aeebfec509a851748f824e9))
* **website:** docs cross-links, inline link rendering & landing polish ([85da8ff](https://github.com/yassinebenameur/probara/commit/85da8ffd60762b44f71b70ab7d12d067a74c94f0))

# [1.0.0-alpha.57](https://github.com/yassinebenameur/probara/compare/v1.0.0-alpha.56...v1.0.0-alpha.57) (2026-07-16)


### Bug Fixes

* **security:** P1 review fixes — location worker auth, NATS auth, secret hardening ([edcd44b](https://github.com/yassinebenameur/probara/commit/edcd44b245fb8c6b2d5c1fc9294189431f85b50e))


### Features

* **auth:** enterprise auth — RBAC, audit log, scoped API keys & OIDC SSO ([09f69f5](https://github.com/yassinebenameur/probara/commit/09f69f599e1530cef42baeedff8f4a20cffbbbf1))
* **locations:** private locations & multi-location checks ([4d391b6](https://github.com/yassinebenameur/probara/commit/4d391b690c9b8bff79c0194ae2011553ba6dcc7c))
* **mesh:** inter-location connectivity mesh — probes, edge state, alerts & UI ([c48af09](https://github.com/yassinebenameur/probara/commit/c48af0995b0e3eb97a8ba0a886e4bdae508b254d))
* **monitors:** MySQL, WebSocket & DNS custom-resolver types — Tier 1 complete ([c68889c](https://github.com/yassinebenameur/probara/commit/c68889ce9c8d84c1dfc0f5d4f23f68249860f252))
* status page custom templates + dependencies graph redesign ([702668c](https://github.com/yassinebenameur/probara/commit/702668c2938118647a0b84fa14eb8e919c52541c))
* **status-page:** redesign editor — live preview, section-first layout, inline monitor picker ([21dcdc2](https://github.com/yassinebenameur/probara/commit/21dcdc20634112ae89c668e683ed3bfd4d04437f))

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
