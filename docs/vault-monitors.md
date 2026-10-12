# Vault monitors

Select **Vault** in the monitor picker under Infrastructure. Works with
HashiCorp Vault and OpenBao. Full field reference: the Monitors docs page
(`website/lib/docs/pages/monitors.ts`, section `vault`).

## What a check does

1. `GET /v1/sys/health` (no token). The body is evaluated, not the status
   code: not initialized, sealed, or removed from the Raft cluster is down; a
   standby / performance standby / DR secondary is up unless
   `require_active` is set.
2. With `token` configured: `GET /v1/auth/token/lookup-self` with
   `X-Vault-Token` (and `X-Vault-Namespace` when set). A non-200 is down (403
   and redirects are errors). The remaining token TTL is recorded and,
   with `token_min_ttl_seconds`, alerts before the monitor token expires.

## Why step 2 matters

`sys/health` is unauthenticated and never audited. When no enabled audit
device can write (full disk under a `file` device, unwritable path, dead
socket/syslog target) Vault fails closed and returns HTTP 500 to every
authenticated request — while `sys/health` keeps answering 200. A plain HTTP
monitor on `sys/health` stays green through the whole outage; the token probe
turns red on the first check.

## Monitor token

`lookup-self` is allowed by Vault's built-in `default` policy, so the token
needs no extra policy. Use a dedicated periodic token or a long-TTL one. A
periodic token needs root/sudo to mint directly, or a token role with
`token_period`:

```sh
vault write auth/token/roles/probara-monitor allowed_policies=default \
  token_period=768h orphan=true
vault token create -role=probara-monitor -display-name=probara-monitor
```

A periodic token still expires if nothing renews it within the period; set
`token_min_ttl_seconds` (e.g. 72h) so Probara tells you before it goes blind.
Each check writes one request/response pair to the audit log.

## Early warning for the disk itself

The monitor detects the outage, not the cause. Add one of:

- an agent monitor metric rule on the audit volume's filesystem utilization;
- a Prometheus monitor on `node_filesystem_avail_bytes{mountpoint="…"}`;
- with Vault telemetry scraped, a Prometheus monitor on
  `increase(vault_audit_log_request_failure[5m])` `eq 0` — this also catches
  one failing device while others keep Vault serving.
