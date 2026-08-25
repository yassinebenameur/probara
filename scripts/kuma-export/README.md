# kuma-export

Pulls the monitor list out of a running [Uptime Kuma](https://github.com/louislam/uptime-kuma)
and writes it in Kuma's own backup format, which Probara's monitor importer
understands.

## When you need it

Kuma 1.x can already write this file itself — **Settings → Backup → Export** —
and that file imports directly. This tool exists for Kuma 2.0+, which removed
the backup button. Kuma has never had a REST API, so the only remaining
machine-readable surface is the internal socket.io API this tool speaks.

## Usage

```sh
go run ./scripts/kuma-export -url https://kuma.internal -user admin
```

| Flag | Meaning |
|---|---|
| `-url` | Base URL of the Kuma instance (required). A path prefix is preserved. |
| `-user` | Kuma username (required). |
| `-pass` | Password. Falls back to `$KUMA_PASSWORD`, which keeps it out of your shell history. |
| `-totp` | Current TOTP code, if the account has two-factor auth enabled. |
| `-out` | Output file, default `kuma-export.json`. `-` writes to stdout. |
| `-include-secrets` | Keep credentials verbatim instead of redacting them. |
| `-insecure` | Skip TLS certificate verification. |
| `-timeout` | Overall deadline, default 45s. |

Then upload the result at **Monitors → Import**. Preview shows what each
monitor became, what was dropped, and what could not be translated at all.

Credentials stay on your machine: nothing is persisted and nothing is sent to
Probara.

## Redaction

By default the tool removes credentials before anything touches disk. It
redacts the *credential*, not the field, so the monitor is still importable:

- Pure-secret fields are dropped: `basic_auth_pass`, `oauth_client_secret`,
  `tlsKey`, `radiusPassword`, `radiusSecret`, `mqttPassword`, `pushToken`.
- `databaseConnectionString` keeps its scheme, user, host, port, path, and
  query but loses the password. The importer builds a database monitor from
  what remains and creates it **disabled**, so it cannot page anyone before
  someone supplies the password.
- `headers` loses credential-bearing entries (`Authorization`, `Cookie`,
  `X-Api-Key`, anything containing `token`/`secret`/`apikey`/`password`) and
  keeps the rest.
- Notifiers are reduced to `id` and `name`. Their configuration holds Slack
  webhooks and SMTP passwords, and notifications are never migrated, so the
  rest is dropped unconditionally — including under `-include-secrets`.

`-include-secrets` keeps the monitor fields verbatim for a migration that needs
no manual step afterwards. The output is then an unencrypted credential file:
treat it accordingly and delete it when you are done.

## Scope

The tool is a transport and performs no mapping. Every decision about which
Probara monitor type a Kuma monitor becomes lives in
`api/internal/services/import/kuma.go`, where it is covered by tests. See the
"Migrating from Uptime Kuma" section of the Administration docs for the
translation table and the list of types that cannot be carried over.
