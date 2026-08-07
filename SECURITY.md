# Security Policy

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues,
pull requests, or discussions.**

Report privately through GitHub's
[private vulnerability reporting](https://github.com/yassinebenameur/probara/security/advisories/new)
("Security" → "Report a vulnerability"). This opens a draft advisory visible
only to you and the maintainers.

Please include as much of the following as you can:

- The type of issue (authentication bypass, SSRF, injection, privilege
  escalation, secret exposure, and so on)
- Affected version or commit, and the affected component (`api`, `worker`,
  `scheduler`, `alerter`, `status-page`, `web`, or the Helm chart)
- Steps to reproduce, or a proof of concept
- The impact you believe it has, including which tenants or roles are affected

### What to expect

- **Acknowledgement** within 5 business days.
- **An initial assessment** — whether we can reproduce it and our severity
  judgement — within 10 business days.
- **Progress updates** at least every 15 days while we work on a fix.
- **Credit** in the advisory and release notes, unless you prefer to stay
  anonymous.

Please give us a reasonable window to ship a fix before public disclosure. We
will coordinate timing with you and publish a GitHub Security Advisory when the
fix is released.

## Supported versions

Probara is pre-1.0. Security fixes land on the latest release only; there are
no maintained backport branches yet. Run a current version.

## Scope

In scope — anything that undermines the platform's security guarantees:

- **Tenant isolation** — reading or writing another tenant's monitors, alerts,
  status pages, or results
- **Authentication and authorization** — login bypass, session or JWT flaws,
  privilege escalation across the superadmin/member and tenant role model,
  API key scope escapes
- **Secret handling** — monitor config credentials leaking through API
  responses, logs, exports, or alert payloads; failures of encryption at rest
- **SSRF** — bypassing the worker's dial guard
  (`worker/internal/worker/dial_guard.go`) to
  reach private address space that policy should block. The guard is a core
  control: probes take arbitrary user-supplied targets by design, so gaps here
  matter.
- **Injection** — SQL injection, template injection in status-page custom
  templates, XSS in the operator UI or public status pages
- **Deployment defaults** — a default in the Helm chart or Compose file that
  leaves a production install exposed

Out of scope:

- Findings that require an already-compromised host or database
- Missing hardening headers or TLS configuration on a deployment you control,
  absent a concrete exploit
- Denial of service through sheer request volume, and rate-limiting gaps
- Vulnerabilities in third-party dependencies with no exploitable path in this
  codebase — report those upstream, though we do want to hear if a dependency
  is exploitable *as we use it*
- The documented local development defaults (`admin` / `change-me`, the dex
  test IdP, the Compose credentials). These are intentionally weak, are meant
  only for local use, and are not a vulnerability. A production deployment path
  that silently keeps them **is** in scope.

## Operator guidance

If you run Probara, note that monitor configurations hold credentials for the
systems you monitor. Set `PROBARA_SECRETS_KEY` on the api, scheduler, worker,
and alerter workloads so those configs are encrypted at rest, and keep the
worker's private-IP blocking enabled except for the CIDRs you deliberately
allow. The [operations documentation](website/lib/docs/pages/operations.ts)
covers key rotation and the rest of the deployment surface.
