import type { DocPage } from "../types";

export const MONITORS_PAGE: DocPage = {
  slug: "monitors",
  group: "Use Probara",
  title: "Monitors",
  description:
    "Configure active, passive, grouped, database, broker, WebSocket, Prometheus query, and synthetic monitors.",
  eyebrow: "Product guide",
  readingTime: "35 min read",
  keywords: [
    "HTTP monitor",
    "synthetic monitoring",
    "database monitoring",
    "monitor configuration",
    "uptime",
  ],
  sections: [
    {
      id: "monitor-model",
      title: "Monitor model",
      intro:
        "A monitor combines a type-specific check configuration with scheduling, state confirmation, [notification routing](/docs/alerting/#routing), dependencies, and optional [execution locations](/docs/locations/).",
      blocks: [
        {
          type: "table",
          columns: ["Field", "Meaning", "Constraints and defaults"],
          rows: [
            ["`name`", "Tenant-visible monitor name", "Required"],
            [
              "`type`",
              "Execution or aggregation implementation",
              "Immutable in normal editing; see supported types below",
            ],
            [
              "`config`",
              "Type-specific JSON object",
              "Validated independently for every type",
            ],
            [
              "`interval_seconds`",
              "Time between scheduled runs or expected passive reports",
              "10–86,400 seconds",
            ],
            [
              "`timeout_seconds`",
              "Maximum active-check duration",
              "Positive and shorter than `interval_seconds` for active types",
            ],
            [
              "`enabled`",
              "Whether the monitor is scheduled/evaluated",
              "Disabled monitors do not execute or maintain availability alerts",
            ],
            [
              "`tags`",
              "Search, dashboard-grouping, and filtering labels",
              "Stored as a tenant-scoped list",
            ],
            [
              "`consecutive_failures_threshold`",
              "Failures required before temporal state becomes down",
              "1–10",
            ],
            [
              "`notification_mode`",
              "Use tenant defaults or monitor-specific routes",
              "`default` or `custom`",
            ],
            [
              "`notification_channels`",
              "Custom channel assignments and escalation delay",
              "Each item has `channel_id` and non-negative `delay_seconds`",
            ],
            [
              "`alert_routing`",
              "Read-only reachability: whether this monitor's alerts notify anyone, and why not — see [notification routing](/docs/alerting/#routing)",
              "Computed per request; never accepted on write",
            ],
            [
              "`depends_on_ids`",
              "Upstream monitors used for root-cause annotation",
              "Tenant-scoped, live monitors; cycles rejected",
            ],
            [
              "`location_ids`",
              "Private worker locations selected for active execution",
              "Not supported for group, agent, or push monitors",
            ],
            [
              "`location_quorum`",
              "Down locations required for aggregate down state",
              "Clamped to 1 through the number of selected locations",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Read responses also expose identity and timestamps, `next_run_at`, the effective current state, maintenance status, member and dependency information, passive credentials where applicable, and per-location state on the detailed monitor view.",
        },
      ],
    },
    {
      id: "supported-types",
      title: "Supported monitor types",
      blocks: [
        {
          type: "table",
          columns: ["Type", "What it verifies", "Scheduled active check"],
          rows: [
            ["`http`", "HTTP response, content, headers, JSON, TLS, and latency", "Yes"],
            ["`ping`", "ICMP reachability", "Yes"],
            ["`dns`", "DNS resolution and optional expected answers", "Yes"],
            ["`grpc`", "Standard gRPC health service status", "Yes"],
            ["`tcp`", "TCP connection and optional TLS handshake", "Yes"],
            ["`sip`", "SIP `OPTIONS` ping, `REGISTER` auth probe, or `INVITE` test call (PDD, answer, ACK/BYE)", "Yes"],
            ["`websocket`", "WebSocket upgrade and optional message exchange", "Yes"],
            ["`prometheus`", "PromQL instant query compared against a numeric threshold", "Yes"],
            ["`redis`", "Redis authentication, `PING`, and optional role", "Yes"],
            ["`postgres`", "PostgreSQL connect and optional query assertion", "Yes"],
            ["`mysql`", "MySQL connect and optional query assertion", "Yes"],
            ["`mongodb`", "MongoDB connectivity, optional topology constraint, and optional clusterMonitor cluster checks", "Yes"],
            ["`rabbitmq`", "AMQP handshake, authentication, and virtual-host access", "Yes"],
            ["`synthetic_api`", "A sequence of templated HTTP API steps", "Yes"],
            ["`synthetic_browser`", "A Chromium browser journey", "Yes"],
            ["`group`", "Derived state from member monitors", "No"],
            ["`agent`", "Host metrics and freshness pushed by an [installed OpenTelemetry collector](/docs/agents/#install)", "No"],
            ["`push`", "Token-based [passive heartbeat](/docs/agents/#push-config) freshness", "No"],
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "Import accepts every type in this table",
          text:
            "All types above can be created through the normal monitor API/UI, and the raw portable import path accepts the same registry of types. Exported secrets are masked, so re-imported monitors that rely on credentials still need those values re-entered before their checks pass.",
        },
      ],
    },
    {
      id: "scheduling-and-state",
      title: "Scheduling, confirmation, and quorum",
      blocks: [
        {
          type: "paragraph",
          text:
            "An active monitor runs on its interval. Both `failure` and `error` outcomes count toward consecutive failure confirmation. Before the threshold it is `suspect`; at the threshold it becomes `down`. Any successful effective result resets the temporal counter and returns it to `up`. Confirmed transitions drive the [availability alert lifecycle](/docs/alerting/#availability).",
        },
        {
          type: "paragraph",
          text:
            "A monitor selected for multiple locations maintains independent location state. The aggregate becomes `down` when down locations reach `location_quorum`; it becomes `degraded` when at least one location is down but the quorum is not met. If no location is down but one is suspect, the aggregate is suspect. Locations that have not reported are not counted as down.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Quorum is failure quorum",
          text:
            "A quorum of 2 means two locations must be down—not that two locations must be up. Disabled or deleted locations are removed from scheduling and the stored quorum is reduced when necessary.",
        },
      ],
    },
    {
      id: "http",
      title: "HTTP monitors",
      intro:
        "HTTP monitors support transport diagnostics and layered assertions on one response.",
      blocks: [
        {
          type: "table",
          columns: ["Variable", "Purpose"],
          rows: [
            ["`url`", "Required absolute `http://` or `https://` target"],
            [
              "`method`",
              "Required: `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD`, or `OPTIONS`",
            ],
            ["`headers`", "Request header map"],
            ["`body`", "Optional request body"],
            [
              "`expected_status`",
              "One accepted HTTP status code between 100 and 599",
            ],
            [
              "`expected_statuses`",
              "Additional individually accepted status codes",
            ],
            [
              "`expected_status_ranges`",
              "Accepted inclusive `{min,max}` ranges between 100 and 599",
            ],
            [
              "`expected_status_classes`",
              "Accepted classes such as `2xx`, up to `5xx`",
            ],
            [
              "`expected_body_substring` / `expected_body_regex`",
              "Legacy single body checks",
            ],
            [
              "`body_assertions`",
              "`contains`, `not_contains`, `regex`, or `not_regex` checks",
            ],
            [
              "`response_header_assertions`",
              "Header name plus `exists`, equality, containment, regex, and negative variants",
            ],
            [
              "`json_assertions`",
              "GJSON path plus existence, equality, text, regex, numeric, or boolean operation",
            ],
            [
              "`display_fields`",
              "Up to 5 `{label, path}` pairs (GJSON path) whose values from the latest response are shown on the monitors list; never assertions",
            ],
            [
              "`case_insensitive`",
              "Optional case-insensitive text comparison where supported",
            ],
            [
              "`max_latency_ms`",
              "Fail the monitor when total response latency exceeds this limit",
            ],
            [
              "`follow_redirects` / `max_redirects`",
              "Redirect policy; maximum is 0–50",
            ],
            [
              "`tls_skip_verify`",
              "Skip server certificate verification for HTTPS; use only for controlled targets",
            ],
            [
              "`tls_min_days_valid`",
              "Open a [`tls_expiry` alert](/docs/alerting/#host-and-mesh) when the leaf certificate has fewer remaining validity days; the check itself still passes",
            ],
            [
              "`tls_server_name`",
              "Override the TLS server name used for verification",
            ],
            ["`tls_ca_pem`", "Additional PEM-encoded certificate authority"],
            [
              "`collect_timing`",
              "Collect detailed DNS, connect, TLS, TTFB, and total timing values",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "When no status expectation is supplied, the accepted default is the `2xx` class. When multiple status criteria are supplied, satisfying any accepted code, range, or class is sufficient. Body, header, JSON, and latency assertions are then evaluated in addition to status. The `tls_min_days_valid` window is the exception: it never fails the check and instead raises a dedicated `tls_expiry` alert (the check still fails if the certificate cannot be inspected at all, e.g. a non-HTTPS URL with the field set).",
        },
        {
          type: "paragraph",
          text:
            "JSON assertions support `exists`, `equals`, `not_equals`, `contains`, `not_contains`, `regex`, `number_gt`, `number_gte`, `number_lt`, `number_lte`, and `bool_is`. Result metrics can include the final URL, redirect count, phase timings, TLS version and cipher, certificate validity, issuer, subject names, expiry, assertion detail, and resolved display values.",
        },
        {
          type: "paragraph",
          text:
            "Display fields put facts from a JSON response on the monitors list, such as a deployed `version` or `checks.db`. Configure them under Show values in monitor list, in the form's Labels & display section. They are resolved on every check, including failed ones, and shown as chips from the latest result (a chip without a label shows only the value), with the full value in the preview panel. A path that is missing or a body that is not JSON shows nothing and never fails the check. Scalars are shown as text and objects or arrays as raw JSON, cut to 64 characters. Each value is stored with its check result for the raw result retention window and is visible to anyone who can read the monitor, so do not point a display field at sensitive data.",
        },
        {
          type: "code",
          language: "json",
          title: "HTTP monitor config",
          code:
            '{\n  "url": "https://api.example.com/health",\n  "method": "GET",\n  "headers": {"Accept": "application/json"},\n  "expected_status_classes": ["2xx"],\n  "json_assertions": [\n    {"path": "status", "op": "equals", "value": "ok"},\n    {"path": "queue_depth", "op": "number_lt", "value": 100}\n  ],\n  "display_fields": [\n    {"label": "ver", "path": "version"}\n  ],\n  "max_latency_ms": 1500,\n  "follow_redirects": true,\n  "max_redirects": 5,\n  "tls_min_days_valid": 14,\n  "collect_timing": true\n}',
        },
      ],
    },
    {
      id: "network-protocols",
      title: "Ping, DNS, gRPC, TCP, and SIP",
      blocks: [
        {
          type: "definitions",
          items: [
            {
              term: "Ping",
              description:
                "Set `host` to a hostname or IP address. The worker performs an ICMP reachability check; firewalls and worker privileges can affect results independently of application health.",
            },
            {
              term: "DNS",
              description:
                "Set `host`, optional `record_type`, optional `expected_answers`, and optional `nameserver`. Supported record types are `A`, `AAAA`, `CNAME`, `TXT`, `MX`, and `NS`; the record type defaults to `A`. A nameserver can be a host or `host:port`, with port 53 used when omitted.",
            },
            {
              term: "gRPC",
              description:
                "Set `host`, optional `port`, optional `service`, and `use_tls`. Probara calls the standard `grpc.health.v1.Health/Check` method and succeeds only for `SERVING`. The default port is 443 with TLS and 80 without it.",
            },
            {
              term: "TCP",
              description:
                "Set `host` and `port`; optionally enable TLS and `tls_skip_verify`. A successful connection, plus a successful TLS handshake when enabled, is healthy. No application payload is exchanged.",
            },
            {
              term: "SIP",
              description:
                "Set `host`, optional `port`, `transport` (`udp`, `tcp`, or `tls`), `method`, and `expected_status`. `method: options` (default) sends a SIP `OPTIONS` availability ping; `method: register` sends a query-style `REGISTER` (no Contact header) that exercises the registrar and its digest authentication without creating, refreshing, or removing bindings. Configure `username`/`password` to answer digest challenges (MD5 and SHA-256, qop=auth) on either method, and `domain` to set the address-of-record for REGISTER. Port defaults to 5060 (5061 for TLS), status to 200; `tls_skip_verify` accepts lab certificates, and `tls_server_name` overrides the name used for SNI and certificate verification (default: `host`; a hostname or IP address, a trailing dot allowed, which the API checks on save). By default (`resolution` unset or `a`) the worker dials the A/AAAA records of `host` at `port`, moving to the next address when one refuses the connection, the network policy blocks it, or, over TCP or TLS, the connection is not established within that address's share of the timeout (what is left divided by the addresses left, at least 2 s; the last address keeps all of it), as dialing the hostname always did; an address that connects but then stays silent is not given up on. `resolution: rfc3263` (the form's \"Locate server via DNS\") finds the server the way a SIP client does (RFC 3263): an IP `host` or an explicit `port` still means A/AAAA only. With `port` unset (0) and `transport` left empty, the worker first reads the domain's NAPTR records and follows the usable ones (flag `s`, an empty regexp, and service `SIP+D2U`, `SIP+D2T` or `SIPS+D2T`, which also serves the probe's `sip:` Request-URI), in order and preference, to the first SRV name that yields a usable target (a name that withdraws the service with a `.` target, fails, or has no resolvable target gives way to the next record); records with a regexp, and any other service, are ignored. NAPTR goes to `nameserver` when one is set, else to the first two nameservers in the worker's `/etc/resolv.conf`, over UDP (TCP when the answer is truncated), within 1.5 s in all; when no nameserver answers, the check records `naptr_status: unavailable` and goes on to SRV. Without a usable NAPTR record, or with `transport` set, the worker reads the domain's SRV records, `_sip._udp`, `_sip._tcp` or `_sips._tcp` for the configured `transport` or, with `transport` left empty, the first of those three that yields a usable target, in that order (a name NAPTR already led to is not asked again). When every published SRV name fails, the check is a `dns` error rather than a fallback to A/AAAA. It tries the targets in priority and weight order (at most four addresses, two per target), and falls back to A/AAAA at the default port, over UDP when no transport is set, when the domain publishes no SRV. It moves to the next address on a refused, failed or blocked connection, a TLS handshake failure (only to another target host, since every address of one host presents the same certificate), a `503`, or no response within that address's share of the timeout (what is left divided by the addresses left, at least 1.5 s). `nameserver` (rfc3263 only; `host` or `host:port`, as on DNS monitors) pins the DNS server, which the worker reaches through its network policy like any target. Under rfc3263 the `OPTIONS` Request-URI is `sip:` plus `domain` (else `host`, with `port` when one is set) rather than `host:port`, and the TLS identity stays `tls_server_name`, else `host`, never an SRV target (RFC 5922): a certificate that names only the SRV target fails verification with a hint to set `tls_server_name`. `tls_min_days_valid` works as on HTTP monitors and requires `transport: tls` explicitly: a certificate with fewer remaining validity days opens a [`tls_expiry` alert](/docs/alerting/#host-and-mesh) while the check itself still passes, the status page shows the remaining days, and only a check that gets an answer without a certificate to inspect fails with `tls: missing certificate info`. The certificate is recorded under `tls_skip_verify` too. No field may contain a carriage return, line feed or NUL, which the API refuses on save and the worker refuses before sending anything. Keys are spelled exactly as documented, here and inside `call`: the API refuses a config key in another case, such as `Method` or `Call`. A check that gets no SIP response is an `error` whose message names the cause first: `dns`, `ssrf_blocked`, `refused`, `connect`, `timeout`, `tls` for a failed TLS handshake, such as an expired, untrusted or mismatched certificate, or `loop` when the probe's own request comes back to it (an echo service or a routing loop answers, not a SIP server). Each request is an RFC 3261 client transaction: over UDP an unanswered request is retransmitted (500 ms, doubling up to 4 s, and every 4 s once a provisional response arrives) until the check timeout, which then reads `timeout: no response to OPTIONS` (or `REGISTER`); TCP and TLS never retransmit. Responses are matched to the request by `Via` branch and `CSeq` method, so a late or stray response, such as a challenge left over from an earlier attempt, is ignored rather than taken as the answer. `Via` and `Contact` carry the worker's real local address and port (`Via` adds `rport` for NAT). Every message of an attempt rides the one connection the worker dialed under its network policy, always to an IP address it resolved itself: the worker never dials an address learned from the server, and it answers requests the server sends during the check on that same connection (200 to an `OPTIONS` keepalive or `INFO`, 481 to a `BYE` or `CANCEL` it holds no dialog for, 488 to an `INVITE` or `UPDATE`, 501 to anything else), never answering its own request reflected back. Every SIP check that gets as far as resolving its host records `metrics_data.sip`, on success, failure and error alike: `method`, `transport` (absent only when DNS failed before rfc3263 chose one), `request_uri`, `call_id` (once a request was sent: search the server's logs for it), the deciding final response's `status_code` and `reason`, its `server` (the `Server` header, else `User-Agent`), `warning` (the `Warning` header) and `reason_header` (`protocol`, `cause`, `text`, preferring the Q.850 entry), `target` (the `host` of the address that decided the check, the configured host or an SRV target, the connected `address`, also after a failed TLS handshake and absent when no connection was made, `resolved_via`: `literal` for an IP, `a` for a hostname lookup, `srv` for an SRV target, `naptr` for an SRV target reached through NAPTR, and `failover` when an earlier address was given up on), `resolution` (`mode`, then under rfc3263 `naptr_status`, which is `ok`, `none`, `unavailable` or `skipped` when an IP `host`, a `port` or a `transport` rules NAPTR out, and `naptr`, the usable records with their `order`, `pref`, `service` and `replacement`; the `srv` records of the SRV name used, and `attempts`: every address tried, in order, with its `transport`, `outcome`, which is `ok`, `503` or the error class, and `ms`), `timing` (`dns_ms` for every NAPTR, SRV and A/AAAA lookup, absent for an IP `host`, then, for the deciding address, `connect_ms`, `tls_handshake_ms`, `first_response_ms` from the first request to the first response of any kind, `response_ms` from the measured request, the authenticated retry when challenged, to its final response, and `total_ms`, which equals `latency_ms`), `retransmissions` and `stray_messages` (responses that matched no request and were ignored), both summed over every address tried, and, after a digest challenge, `auth` (`challenge` 401 or 407, `algorithm`, `rounds`, and `auth_ms`, the time the challenge added). Over TLS it adds `tls`, the server certificate with the same keys as an HTTP check's (`version`, `cipher_suite`, `server_name`, `not_before`, `not_after`, `days_until_expiry`, `subject`, `issuer`, `dns_names`, …), and `assertions_failed` lists the check's own failed rules. Retrieve them with `GET /api/v1/monitors/{id}/metrics-snapshots`; the monitor's detail page shows the latest in its Server panel.",
            },
            {
              term: "SIP test call (INVITE)",
              description:
                "Set `method: invite` and a `call` object to place a real test call through `host`, which acts as the outbound proxy: every request goes there, with the monitor's transport, `resolution`, TLS settings and digest credentials, whatever the Request-URI names. `call.number` calls `sip:<number>@<domain or host>` (on a test call `domain` must be a bare hostname or IP address, without port, parameters or headers; with `;user=phone` for a telephone number, digits with an optional leading `+`; `sips:` when `transport` is `tls`); `call.target_uri` gives a full `sip:` or `sips:` Request-URI instead: its user part must be the dialled string alone, in the `number` character set with `#` written `%23` (user parameters such as `;phone-context` and a `:password` are refused, since they could change what the far end dials), `?` headers and `maddr` are refused, and a `sips:` URI requires `transport: tls`, as RFC 3261 asks TLS of every hop. `from_user` (default `username`, else `probara`), `from_display` and `from_domain` (default `domain`, else `host`) set the caller ID, `probe_header` adds `X-Probara-Probe: 1`, and `sdp_address` replaces the offer's `o=`/`c=` address for a worker behind NAT. `call.mode: answer` (the default) sends the INVITE with an RTP/AVP audio offer (PCMU, PCMA and telephone-event on a bound port pair; no media is ever sent), ACKs the 200, holds the call for `hold_ms` (0-10000) and hangs up with BYE. `mode: ring` succeeds on the first `18x` and CANCELs, so the call is never answered; a 200 that comes anyway is ACKed and BYE'd at once, still a success, with the warning `answered in ring mode — billable`. A 401 or 407 is answered once (twice when the server marks the nonce `stale`), with one credentials header per challenged realm; every retry keeps the credentials of each realm answered so far (at its next nonce count), the ACK of the 200 repeats the INVITE's credentials, and a challenged BYE is answered with the next nonce count. A challenge for a realm never answered before that arrives after the first round (a further hop, such as a proxy and then the far end each asking) fails the check as `auth: … challenged realm \"…\" after the first round; only one authentication round is supported (multi-hop authentication)`: multi-hop authentication is not supported. A `3xx` is never followed (`redirect: 302 Moved Temporarily (not followed; contact …)`), `420`/`421` reads `extension: far end requires 100rel/timer (not supported)` and `488` reads `media: offer rejected (488); trunk may require SRTP (RTP/AVP only)`. In answer mode any other final but a 2xx fails the check as `status: 486 Busy Here (Q.850 17)`; set `expected_status` (200-699, not allowed in ring mode) to require one exact final instead, such as 486 for a busy test line. No answer within the timeout minus the hold fails with `answer: no answer within …` and the INVITE is CANCELled; in ring mode a call that only gets `100` fails with `pdd: no ringing within …`; no response at all is the error `timeout: no response to INVITE`, and no CANCEL is sent until a provisional arrives (RFC 3261 §9.1). `call.warn_pdd_ms`/`call.max_pdd_ms` and, in answer mode, `call.warn_answer_ms`/`call.max_answer_ms` bound post-dial delay and answer time: a max fails the check (`pdd: 4200ms > 3000ms`), a warn keeps it a success and records `warnings` and `latency_warn_ms`. `latency_ms` is the time to the decisive response (the 200, or the final, in answer mode; the first `18x` in ring mode), and `http_status` is the final INVITE code, in ring mode the `18x` (or the 200 when answered), never the BYE's. The worker fails over to the next resolved address only when the connection cannot be established (a refused, failed or blocked connect, or a failed TLS handshake, or one not completed within that address's share of the timeout, the last address keeping all of it) or on a `503` before any provisional: once the INVITE is sent on a connection, the address is never abandoned, neither for silence nor for an error on that connection such as a UDP port unreachable, since it may already be ringing someone and a second address would place a second call. The call adds `metrics_data.sip.call`: `mode`, `trying_ms`, `pdd_ms` (from the INVITE that drew it, the authenticated one when challenged, to the first `18x`, or to the 200 when none came; `pdd_source` says which), `setup_pdd_ms` and `setup_ms` (from the first INVITE, so they include the digest round trip), `answer_ms`, `hold_ms`, `bye_ms`, `provisional` (each code with its `at_ms`), `early_media`, `answered`, `forked_answers`, `route_hops`, `require_100rel`, `answered_by` (the connected address, Contact host and Record-Route hosts), `sdp_answer` (`codec`, `payload_type`, `remote_media`, `direction`), `rtp_rx_packets`, and `teardown` with `teardown_status`: `bye_ok`, `bye_481`, `bye_<code>`, `bye_unconfirmed`, `cancelled`, `cancel_unconfirmed`, `answered_after_cancel`, `remote_bye` (the callee hung up first), `no_provisional` or `none`. An unconfirmed teardown leaves the status as it is and adds a warning. After the result the worker keeps the call's connection open for up to 32 s to ACK and BYE an answer that arrives late or from another fork. A test call needs `interval_seconds` of at least 60 and `timeout_seconds` of at most 20, with `hold_ms` plus 2 s fitting in the timeout (the worker also clamps the timeout to 20 s and the hold to 10 s); only a tenant admin or superadmin, signed in, can create, change (pausing included), run or test one, and API keys are refused whatever their scope, as is an imported row from anyone else. A monitor cannot switch between a test call and `OPTIONS`/`REGISTER`, or change type to or from one: create a new monitor. Every request rides the connection to `host`: the answer's Contact and Record-Route only fill in-dialog Request-URIs and Route headers, and the SDP answer's media address is recorded, never contacted, so a far end that answers over UDP from another address, or sends its in-dialog requests from elsewhere, is not supported. A worker places calls only while `SIP_CALL_ENABLED` allows it (on by default on platform workers, off on private-location workers) and, when `SIP_CALL_ALLOWED_DESTINATIONS` is set, only to a matching called number; a refused call is an `error` (`sip_call: disabled on this worker`, `policy: destination not allowed by worker policy`) and sends nothing. Each redelivered check job of a test call is dropped rather than run again, so a call is placed at most once per scheduled check. The monitor's detail page shows the latest call in its Server panel and charts post-dial delay and answer time over the overview's selected range, one point per call, with the warn and max thresholds drawn as reference lines; once a call was challenged, a second line measures from the first INVITE, digest round trip included. A call that stopped earlier (no ringing, no answer, no response) leaves a gap, ring mode charts answer time only for calls that were answered anyway, longer ranges keep one call per time bucket, and ranges beyond the 30-day raw retention show the last 30 days.",
            },
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "Protocol health is deliberately narrow",
          text:
            "A successful TCP connection does not prove an application protocol is healthy. A gRPC monitor requires the standard health service. A SIP `INVITE` test call completes call signalling with a valid audio offer, but no RTP is sent or analysed: no jitter, loss, MOS or DTMF. The offer is RTP/AVP only, so SRTP-only trunks answer 488.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "SIP test calls are real calls",
          text:
            "Every check of an `INVITE` monitor places a real call that the carrier may bill: at the 60 s floor from three locations that is 4,320 calls a day, and `Run now` (one call per selected location) and `Test` dial too. Call a dedicated test number that accepts as many concurrent calls as the monitor has locations, since concurrent calls to a single-channel number answer 486. Repeated authentication failures from several locations every minute can get the probes' addresses banned by a carrier's fail2ban. There is no per-tenant call budget or run-now cooldown yet; the interval floor, the tenant-admin gate and the workers' `SIP_CALL_*` settings are the limits. In the operator UI the form's `Test call (INVITE)` method is offered to tenant admins only and shows this estimate (calls a day across the monitor's locations) next to the call settings; test-call monitors carry a `CALL` type badge instead of `SIP` in monitor lists (status pages keep `SIP`); `Test call` and the monitor page's `Run check` ask for confirmation before dialing; and for anyone but a tenant admin `Run check`, `Pause`/`Resume` and `Clone` are hidden and the settings are read-only.",
        },
      ],
    },
    {
      id: "websocket",
      title: "WebSocket monitors",
      blocks: [
        {
          type: "table",
          columns: ["Variable", "Purpose"],
          rows: [
            ["`url`", "Required `ws://` or `wss://` endpoint"],
            ["`headers`", "Optional upgrade request headers"],
            ["`tls_skip_verify`", "Disable WSS certificate verification"],
            ["`send_message`", "Optional text frame sent after the upgrade"],
            [
              "`expected_substring`",
              "Require the first received message to contain this text",
            ],
            [
              "`max_latency_ms`",
              "Fail if connection/exchange latency exceeds the maximum",
            ],
            [
              "`warn_latency_ms`",
              "Record a warning metric below the hard maximum without failing the monitor",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Without a message or expected substring, a successful WebSocket upgrade is sufficient. With an expectation, the worker reads the first response message and performs a substring check.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Header values are secret material",
          text:
            "WebSocket header values are encrypted when a secrets key is configured and returned as `***` through the API. Header names remain visible.",
        },
      ],
    },
    {
      id: "prometheus",
      title: "Prometheus query monitors",
      intro:
        "A Prometheus monitor turns any PromQL instant query into an uptime signal. The worker POSTs the query to `/api/v1/query` on the configured base URL and compares every returned float sample against a threshold.",
      blocks: [
        {
          type: "table",
          columns: ["Field", "Purpose"],
          rows: [
            ["`url`", "Required `http://` or `https://` Prometheus base URL, including any reverse-proxy prefix. Credentials, query parameters, and fragments are rejected."],
            ["`query`", "Required PromQL expression, up to 16 KiB. Syntax is validated by Prometheus when the worker runs it."],
            ["`operator`", "Required comparison: `gt`, `gte`, `lt`, `lte`, `eq`, or `ne`"],
            ["`threshold`", "Required finite number in the query's own units; zero and negative values are valid"],
            ["`no_data_status`", "Result when the query returns no samples: `failure` (default), `error`, or `success`"],
            ["`auth_type`", "`none` (default), `basic` with `username` and `password`, or `bearer` with `bearer_token`"],
          ],
        },
        {
          type: "paragraph",
          text:
            "The monitor is healthy only when **every** returned sample satisfies the comparison, so an instant vector with several series is treated as several assertions. Use PromQL aggregation such as `sum`, `max`, or `min` when one value should decide. Scalar and instant-vector float results are supported; range vectors, string results, and native histogram samples report an error.",
        },
        {
          type: "table",
          columns: ["Query", "Healthy condition"],
          rows: [
            ["`up{job=\"api\"}`", "`eq` 1"],
            ["`sum(queue_depth)`", "`lt` 1000"],
            ["`100 * sum(rate(http_requests_total{status=~\"5..\"}[5m])) / sum(rate(http_requests_total[5m]))`", "`lt` 5"],
          ],
        },
        {
          type: "list",
          items: [
            "NaN and infinite samples, including division by zero, report an error rather than a threshold verdict.",
            "Query warnings from Prometheus indicate potentially partial evaluation and report an error, never a healthy result.",
            "HTTP errors, malformed responses, redirects, and connection failures report an error. Redirects are never followed and HTTPS certificates are always verified.",
            "Responses are capped at 2 MiB; aggregate large queries rather than relying on truncation.",
            "The worker's SSRF policy and allowed CIDRs apply, including DNS resolution at connection time. Pick a private location whose worker can reach the endpoint when Prometheus is internal.",
          ],
        },
        {
          type: "paragraph",
          text:
            "Check results record the sample count, the number of samples outside the threshold, and up to twenty numeric values. Labels and raw upstream error bodies are not stored. **Test query** in the form runs the current configuration through a worker without saving; when editing, saved masked credentials are resolved for that monitor.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Prefer a numeric query plus a Probara threshold",
          text:
            "A PromQL filter such as `up == 0` returns an empty vector when everything is healthy, which then falls under `no_data_status`. Querying the numeric value and expressing the threshold in Probara is easier to read and preview.",
        },
      ],
    },
    {
      id: "data-services",
      title: "Redis, PostgreSQL, MySQL, MongoDB, and RabbitMQ",
      intro:
        "Data-service monitors perform a real protocol handshake. Database monitors can optionally run a read query and assert its first returned value.",
      blocks: [
        {
          type: "table",
          columns: ["Type", "Connection variables", "Additional verification"],
          rows: [
            [
              "`redis`",
              "`connection_string` (`redis://` or `rediss://`) or `host`, `port` (6379), `username`, `password`, `db` (0–15), TLS",
              "`PING` and optional `expected_role` of `master` or `replica`",
            ],
            [
              "`postgres`",
              "PostgreSQL URI/key-value DSN or `host`, `port` (5432), `database` (`postgres`), `username`, `password`, `ssl_mode`",
              "Optional `query` and first-column value assertion",
            ],
            [
              "`mysql`",
              "MySQL URI/driver DSN or `host`, `port` (3306), `database`, `username`, `password`, TLS",
              "Optional `query` and first-column value assertion",
            ],
            [
              "`mongodb`",
              "`mongodb://` / `mongodb+srv://` URI or `host`, `port` (27017), paired username/password, `auth_source`, TLS",
              "Optional `replica_set` topology and reachable-primary requirement; optional cluster checks via `collect_replication`, `collect_connections`, `collect_cache`, `collect_memory`, `collect_network`, `collect_cpu`",
            ],
            [
              "`rabbitmq`",
              "`amqp://` / `amqps://` URI or `host`, port (5672 or 5671 with TLS), username/password, `vhost`",
              "AMQP handshake, authentication, and virtual-host access; it does not publish a message",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "PostgreSQL and MySQL query assertions use `query_value_op` with `equals`, `not_equals`, `contains`, or numeric comparison operations against the first column of the first returned row. A configured query must return a row.",
        },
        {
          type: "paragraph",
          text:
            "These monitors share optional `max_latency_ms` and `warn_latency_ms`. The maximum is a hard failure; the warning threshold annotates metrics without changing a successful check to failure.",
        },
        {
          type: "paragraph",
          text:
            "MongoDB monitors can additionally enable per-feature cluster checks, each an individually toggleable read-only admin command: `collect_replication` runs `replSetGetStatus` (member states, health, and replication lag), while `collect_connections`, `collect_cache`, `collect_memory`, `collect_network`, and `collect_cpu` read their sections from a single `serverStatus` call (connections, WiredTiger cache, resident/virtual memory, network I/O and opcounters, and — on Linux servers — mongod process CPU time; host-level CPU is agent-monitor territory). Both commands are covered by MongoDB's built-in `clusterMonitor` role — no `clusterAdmin`, `root`, or write privileges. When the monitoring user lacks the role, the affected data is skipped and flagged in the check's metrics (`unavailable`) without failing the check; on a standalone server the replication check reports \"not a replica set\". Replication lag supports the same warn/max split as latency: `warn_replication_lag_seconds` annotates, `max_replication_lag_seconds` fails the check and flows through normal availability alerting — and it fails closed, so if replication status becomes unreadable (role revoked, command timeout, standalone target, no primary) the check fails rather than silently passing.",
        },
        {
          type: "paragraph",
          text:
            "The monitor detail page charts the collected cluster metrics over the overview's selected range — replication lag (with the warn/max thresholds drawn as reference lines), connections, WiredTiger cache, memory, and, for the cumulative operation, network, and process-CPU counters, per-second rates derived between consecutive snapshots. Longer ranges are downsampled to at most 720 snapshots (the newest check per time bucket), so gauges become point samples while rates stay averaged across the skipped checks. The snapshots come from raw check results, so ranges beyond the 30-day raw retention show the last 30 days.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Granting clusterMonitor",
          text:
            "`db.grantRolesToUser(\"monitoring\", [{ role: \"clusterMonitor\", db: \"admin\" }])` is the only grant the cluster checks need. Without it the basic connect/ping/latency monitoring keeps working unchanged.",
        },
        {
          type: "table",
          columns: ["TLS variable", "Meaning"],
          rows: [
            [
              "`tls_enabled`",
              "Enable transport TLS where the monitor is not controlled by a PostgreSQL `ssl_mode`",
            ],
            ["`tls_skip_verify`", "Skip server certificate verification"],
            ["`tls_ca_pem`", "Additional trusted CA certificate(s)"],
            ["`tls_client_cert_pem`", "Client certificate for mutual TLS"],
            [
              "`tls_client_key_pem`",
              "Client private key; must be paired with the client certificate",
            ],
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Use read-only query credentials",
          text:
            "Optional queries are sent as configured. Give Probara a minimally privileged, read-only database account and use bounded diagnostic queries. The check timeout is not a substitute for database-side query limits.",
        },
      ],
    },
    {
      id: "synthetic-api",
      title: "Synthetic API journeys",
      intro:
        "A synthetic API monitor runs 1–20 ordered HTTP steps with variables, assertions, and extraction from earlier responses.",
      blocks: [
        {
          type: "table",
          columns: ["Level", "Variables"],
          rows: [
            [
              "Journey",
              "`base_url`, `failure_mode`, `variables`, and ordered `steps`",
            ],
            [
              "Step",
              "Unique `id`, optional `name`, `request`, `assert`, and `extract` definitions",
            ],
            [
              "Request",
              "`method`, `url`, headers, body, 1–300 second timeout, redirect policy, maximum 0–50 redirects",
            ],
            [
              "Extraction",
              "Globally unique variable `name`, source `json` or `header`, source path/name, and optional `sensitive` marker",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "URLs may be absolute, or relative when `base_url` is present. Static and extracted variables are referenced with `{{variable_name}}` in subsequent supported fields. `failure_mode` is `fail_fast` by default; use `continue` when later diagnostic steps should still execute after a failed step.",
        },
        {
          type: "paragraph",
          text:
            "Assertions can target status, response headers, body text, or JSON paths. Status supports equality, inequality, and membership. Text and header checks support existence, equality, containment, regular expressions, and negative forms. JSON checks additionally support numeric and boolean operations.",
        },
        {
          type: "code",
          language: "json",
          title: "Two-step API journey",
          code:
            '{\n  "base_url": "https://api.example.com",\n  "failure_mode": "fail_fast",\n  "variables": {"user": "monitor@example.com"},\n  "steps": [\n    {\n      "id": "login",\n      "request": {\n        "method": "POST",\n        "url": "/login",\n        "headers": {"Content-Type": "application/json"},\n        "body": "{\\"email\\":\\"{{user}}\\"}"\n      },\n      "assert": [\n        {"target": "status", "op": "equals", "value": 200}\n      ],\n      "extract": [\n        {"name": "token", "from": "json", "path": "token", "sensitive": true}\n      ]\n    },\n    {\n      "id": "profile",\n      "request": {\n        "method": "GET",\n        "url": "/me",\n        "headers": {"Authorization": "Bearer {{token}}"}\n      },\n      "assert": [\n        {"target": "json", "path": "email", "op": "equals", "value": "{{user}}"}\n      ]\n    }\n  ]\n}',
        },
        {
          type: "paragraph",
          text:
            "Result metrics include completed-step count, total latency, and the failed step identifier. Give every step a stable unique ID so failures remain intelligible after renaming display labels.",
        },
      ],
    },
    {
      id: "synthetic-browser",
      title: "Synthetic browser journeys",
      intro:
        "Browser monitors run 1–12 ordered actions in Chromium through Playwright.",
      blocks: [
        {
          type: "table",
          columns: ["Action", "Required variables", "Behavior"],
          rows: [
            ["`goto`", "`url`", "Navigate to a page"],
            ["`click`", "`selector`", "Click the matching element"],
            ["`fill`", "`selector`, `value`", "Replace an input value"],
            ["`wait_for`", "`selector`", "Wait until an element is available"],
            ["`assert_visible`", "`selector`", "Require an element to be visible"],
            [
              "`assert_text`",
              "`selector`, `value`",
              "Require the selected element to contain expected text",
            ],
            ["`assert_url`", "`value`", "Require the current URL to match"],
          ],
        },
        {
          type: "paragraph",
          text:
            "Journey variables include `start_url`, optional device profile, `failure_mode`, static `variables`, artifact settings, and steps. Each step has a unique ID and a 1–180 second timeout. The configuration model exposes screenshot, Playwright trace, and HAR-on-failure switches, but the current worker implements screenshots only; trace and HAR requests produce warnings rather than artifacts.",
        },
        {
          type: "paragraph",
          text:
            "Failure screenshots are exposed by the authenticated [monitor artifact endpoint](/docs/api/#monitor-endpoints) when the result contains a valid stored artifact path. Artifact availability depends on shared worker/API storage and retention. Do not build automation that expects trace or HAR files until worker support is implemented.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Selectors are part of your monitoring contract",
          text:
            "Prefer stable application-owned attributes over visual CSS structure. A browser journey can fail because the user interface changed even when the underlying API is healthy; pair it with narrower protocol monitors for diagnosis.",
        },
      ],
    },
    {
      id: "groups",
      title: "Monitor groups",
      blocks: [
        {
          type: "paragraph",
          text:
            "A `group` monitor stores member monitor IDs instead of executing a check. It is down when any enabled, non-deleted member is down. It is up when all effective member states are up, suspect, or degraded; otherwise it is unknown.",
        },
        {
          type: "definitions",
          items: [
            {
              term: "`per_monitor` rollup",
              description:
                "The default for newly created groups. Member monitors alert independently and the group does not create an additional availability alert.",
            },
            {
              term: "`group` rollup",
              description:
                "Suppresses member availability alerts covered by that group and creates one group-level availability alert. Suppression ignores the group's own enabled flag, so pausing such a group silences its members too — see [group alert rollup](/docs/alerting/#group-rollup).",
            },
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "Groups are not locations or dependencies",
          text:
            "A group presents and rolls up a set of peers. A location controls where a check runs. A dependency expresses an upstream causal relationship used for [root-cause annotation](/docs/dependencies/#root-cause).",
        },
      ],
    },
    {
      id: "secrets",
      title: "Secret fields and masked updates",
      blocks: [
        {
          type: "paragraph",
          text:
            "When `PROBARA_SECRETS_KEY` is configured, verified monitor secret handling covers `password`, `connection_string`, and `tls_client_key_pem` for Redis, PostgreSQL, MySQL, MongoDB, and RabbitMQ, the SIP digest `password` (also used for INVITE test calls), the Prometheus `password` and `bearer_token`, plus every WebSocket header value. API reads replace protected values with `***`.",
        },
        {
          type: "list",
          items: [
            "Submit `***` again to preserve the already stored value.",
            "Omit the field entirely and the stored value is preserved as well, so an update that resubmits a config read back from the API cannot destroy a credential it was never shown.",
            "Submit a new value to replace and encrypt it.",
            "Submit an empty string to clear a protected field. That is the only spelling that removes a stored secret.",
            "WebSocket header names remain visible while their values are masked. Submitting the `headers` object edits it key by key, so a key left out of a submitted object is removed; omitting the whole object keeps the stored headers.",
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Masking is field-specific",
          text:
            "Do not assume every arbitrary config string is automatically secret. Use the product's designated protected fields and keep credentials out of names, tags, URLs, assertion messages, and other visible configuration.",
        },
      ],
    },
    {
      id: "operations",
      title: "Test, run, inspect, and delete",
      blocks: [
        {
          type: "definitions",
          items: [
            {
              term: "Test configuration",
              description:
                "Executes a provided configuration without creating or updating a monitor. It is compute-only and does not write result history or trigger alert lifecycle.",
            },
            {
              term: "Run now",
              description:
                "Queues an existing monitor for normal execution. The returned result is persisted and can change state and alerts. Group, push and agent monitors never run a check of their own, so Run now refuses them.",
            },
            {
              term: "Results",
              description:
                "Raw chronological check records. List calls accept a limit and optional RFC 3339 `since` timestamp.",
            },
            {
              term: "Analytics",
              description:
                "Summaries, time series, and downtime periods for `1h`, `6h`, `24h`, `7d`, `30d`, `90d`, or `365d`.",
            },
            {
              term: "Delete history",
              description:
                "Removes stored results for a monitor without deleting the monitor definition.",
            },
            {
              term: "Delete monitor",
              description:
                "Soft-deletes the monitor immediately. Scheduler cleanup later purges dependent historical data.",
            },
          ],
        },
        {
          type: "paragraph",
          text:
            "Analytics report uptime, SLA/availability summary fields, downtime duration, average/median/p95/latest latency, series data, downtime periods, source, coverage start, and whether the requested window is partially covered. Long ranges combine rollups with the current raw tail. The summary carries `has_data`: when no checks ran in the window the percentage fields are meaningless zeros and clients must render a no-data state, never 0% or 100%. It also carries `method`: `interval` means `availability_pct` is a time integration over the monitor's state timeline — unknown and paused time leave the denominator and surface as `coverage_pct`, and downtime inside maintenance windows counts as planned rather than unavailability; `sampled` means the window predates the timeline and the legacy success/total rate stands in.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Deleting history is irreversible",
          text:
            "History deletion removes the evidence used for charts and long-term analysis. Export or retain external telemetry first if you need it for compliance or post-incident review.",
        },
      ],
    },
    {
      id: "bulk-and-portability",
      title: "Bulk changes, import, and export",
      blocks: [
        {
          type: "list",
          items: [
            "Bulk alerting changes can update notification mode and channel assignments across selected monitors.",
            "Bulk delete soft-deletes selected monitors and schedules cleanup.",
            "Import preview parses and validates without creating monitors. Execution reports a per-row outcome rather than treating every file as all-or-nothing.",
            "JSON, YAML, and CSV formats are accepted, including common wrapper keys and a versioned portable YAML bundle.",
            "Duplicate matching is case-insensitive on the combination of monitor name and type; duplicates are skipped.",
            "Groups are resolved in a second pass by member name, and ambiguous or missing names are reported.",
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Secrets do not round-trip",
          text:
            "Raw portable import accepts every monitor type in the live registry, including Redis, PostgreSQL, MySQL, MongoDB, RabbitMQ, and WebSocket. Exported masked secrets are not usable credentials, so re-enter passwords, connection strings, and client keys after import; verify every preview before execution.",
        },
      ],
    },
  ],
};
