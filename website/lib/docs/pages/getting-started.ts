import type { DocPage } from "../types";

export const GETTING_STARTED_PAGE: DocPage = {
  slug: "getting-started",
  group: "Start here",
  title: "Getting started",
  description:
    "Run Probara locally, create the first administrator, and execute your first monitor.",
  eyebrow: "Quickstart",
  readingTime: "12 min read",
  keywords: [
    "installation",
    "local development",
    "Docker",
    "first monitor",
    "bootstrap",
  ],
  sections: [
    {
      id: "what-you-will-run",
      title: "What you will run",
      intro:
        "Probara is a multi-service monitoring platform. The preferred development commands start PostgreSQL and NATS, migrate the database, run the backend services, and launch the Next.js application.",
      blocks: [
        {
          type: "definitions",
          items: [
            {
              term: "API",
              description:
                "Owns authentication, tenant-scoped CRUD, monitor administration, imports, dashboard data, passive check ingestion, and administrative APIs.",
            },
            {
              term: "Scheduler and workers",
              description:
                "The scheduler claims due monitors and publishes jobs; workers perform active checks and return results through NATS.",
            },
            {
              term: "Alerter",
              description:
                "Evaluates stored state, opens and resolves alerts, dispatches delayed notifications, and manages incidents.",
            },
            {
              term: "Status-page service",
              description:
                "Renders public status pages and serves their data and server-sent event streams.",
            },
            {
              term: "Web application",
              description:
                "The separate Next.js interface used to configure and operate the platform.",
            },
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "Two supported local workflows",
          text:
            "`make start-all-local` runs application services as local processes. `make start-all` builds and runs them with Docker Compose. Use the matching stop or restart target for the workflow you chose.",
        },
      ],
    },
    {
      id: "prerequisites",
      title: "Prerequisites",
      blocks: [
        {
          type: "list",
          items: [
            "Docker with Docker Compose v2 for PostgreSQL, NATS, and the Docker-backed workflow.",
            "Go 1.23-compatible tooling. The repository currently declares Go 1.23 and a Go 1.23.4 toolchain.",
            "A current Node.js LTS release and npm for the web application.",
            "GNU Make and a Bash-compatible shell for the repository scripts. On Windows, run the Make targets from an environment that provides these tools.",
          ],
        },
        {
          type: "code",
          language: "bash",
          title: "Clone and prepare configuration",
          code: "git clone <your-repository-url>\ncd probara\ncp .env.example .env",
        },
        {
          type: "paragraph",
          text:
            "Never commit `.env` or generated credentials. Set both `ADMIN_JWT_SECRET` (a random value of 32 or more characters is recommended) and `PUBLIC_BASE_URL` in `.env` before either startup workflow. Docker Compose ships an insecure hardcoded default secret and the local script falls back to an insecure placeholder when the value is unset, so override it explicitly for anything beyond throwaway local development. The [configuration reference](/docs/configuration/#common-service-variables) documents every environment variable.",
        },
      ],
    },
    {
      id: "local-process-workflow",
      title: "Start the local-process workflow",
      intro:
        "This is the fastest path for backend development because only PostgreSQL and NATS run in containers.",
      blocks: [
        {
          type: "callout",
          tone: "warning",
          title: "Populate .env before the first start",
          text:
            "The local script falls back to an insecure hardcoded placeholder when `ADMIN_JWT_SECRET` is unset, and it never sets `PUBLIC_BASE_URL`. Explicitly set `ADMIN_JWT_SECRET` and `PUBLIC_BASE_URL` in `.env` so the stack does not run on placeholder or missing values.",
        },
        {
          type: "code",
          language: "bash",
          title: "Start",
          code: "make start-all-local",
        },
        {
          type: "list",
          ordered: true,
          items: [
            "Starts PostgreSQL and NATS with Docker Compose.",
            "Bootstraps local database access and validates the Go toolchain.",
            "Runs the database migrations with `go run ./cmd/migrate`.",
            "Builds the installable host-agent artifacts and starts the Go services as local processes.",
            "Launches the Next.js web application.",
          ],
        },
        {
          type: "paragraph",
          text:
            "If the local startup script runs with `ADMIN_JWT_SECRET` unset, it falls back to a hardcoded development placeholder value. That fallback is an implementation convenience, not a substitute for populating `.env` with a real secret.",
        },
        {
          type: "code",
          language: "bash",
          title: "Stop or restart the same workflow",
          code: "make stop-all-local\nmake restart-all-local",
        },
      ],
    },
    {
      id: "docker-workflow",
      title: "Start the Docker-backed workflow",
      intro:
        "Use this workflow when you want the backend service images and Compose topology to match a [packaged deployment](/docs/deployment/#docker-compose) more closely.",
      blocks: [
        {
          type: "callout",
          tone: "warning",
          title: "Required configuration",
          text:
            "Docker Compose ships an insecure hardcoded `ADMIN_JWT_SECRET` default that you should override for anything beyond local development. Set `ADMIN_JWT_SECRET` to a random value of 32 or more characters and set `PUBLIC_BASE_URL` to the API origin that browsers, agents, remote workers, and generated links can actually reach.",
        },
        {
          type: "code",
          language: "dotenv",
          title: ".env",
          code:
            "ADMIN_JWT_SECRET=replace-with-a-random-secret-at-least-32-characters\nPUBLIC_BASE_URL=http://localhost:8080",
        },
        {
          type: "code",
          language: "bash",
          title: "Start, stop, or restart",
          code: "make start-all\nmake stop-all\nmake restart-all",
        },
        {
          type: "paragraph",
          text:
            "Do not mix the stop targets. `make stop-all-local` cleans up local Go processes, while `make stop-all` stops the Docker-backed application services.",
        },
      ],
    },
    {
      id: "default-endpoints",
      title: "Default local endpoints",
      blocks: [
        {
          type: "table",
          columns: ["Component", "Default endpoint", "Purpose"],
          rows: [
            ["Web application", "`http://localhost:3000`", "Main product UI"],
            ["API", "`http://localhost:8080`", "Authenticated and public API routes"],
            [
              "Public status pages",
              "`http://localhost:8082`",
              "Rendered public pages, data, and SSE",
            ],
            ["NATS monitoring", "`http://localhost:8222`", "Local broker diagnostics"],
            ["API metrics", "`http://localhost:8080/metrics`", "Prometheus metrics"],
            [
              "Scheduler metrics",
              "`http://localhost:9091/metrics`",
              "Prometheus metrics",
            ],
            [
              "Worker metrics",
              "`http://localhost:9092/metrics`",
              "Prometheus metrics",
            ],
            [
              "Status-page metrics",
              "`http://localhost:9093/metrics`",
              "Prometheus metrics",
            ],
            [
              "Alerter metrics",
              "`http://localhost:9094/metrics`",
              "Prometheus metrics",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Backend services also expose `/healthz` and `/readyz`. A healthy process is not necessarily ready: [readiness](/docs/operations/#health-readiness) includes dependencies the service needs to perform work.",
        },
      ],
    },
    {
      id: "first-administrator",
      title: "Create the first administrator",
      intro:
        "A new installation exposes a one-time bootstrap flow. The first active account becomes the platform superadministrator.",
      blocks: [
        {
          type: "list",
          ordered: true,
          items: [
            "Open the web application at `http://localhost:3000`.",
            "Follow the bootstrap prompt and create the initial account.",
            "Use a username of 3–64 characters containing letters, numbers, `.`, `_`, or `-`.",
            "Use a password between 12 and 128 characters.",
            "Sign in and select the initial tenant context.",
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Bootstrap closes after first use",
          text:
            "The public first-user endpoint is only valid while no users exist. Treat initial bootstrap as an installation step, not a recurring user-management path.",
        },
        {
          type: "paragraph",
          text:
            "An empty installation configured for [OIDC](/docs/administration/#oidc) can also bootstrap through the first successful just-in-time OIDC login. Subsequent user and tenant access is governed by platform roles and tenant memberships.",
        },
      ],
    },
    {
      id: "first-monitor",
      title: "Create and run the first monitor",
      blocks: [
        {
          type: "list",
          ordered: true,
          items: [
            "Open `Monitors` and choose a new [HTTP monitor](/docs/monitors/#http).",
            "Enter a public `http://` or `https://` URL, a check interval, and a timeout shorter than that interval.",
            "Use `Test` to perform a compute-only check before saving. A test result is returned immediately and is not added to monitor history.",
            "Save the monitor, then choose `Run now` to enqueue a real check. This result is persisted and participates in state and alert evaluation.",
            "Review the current state, result history, timings, and analytics after the worker reports.",
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "Private destinations are not blocked by default",
          text:
            "Workers only reject loopback, private, link-local, and reserved destinations when `HTTP_BLOCK_PRIVATE_IPS` is enabled; it defaults to `false`. Enable it on internet-facing workers, and for trusted internal monitoring prefer a narrow `HTTP_ALLOWED_CIDRS` allowlist on a [dedicated location](/docs/locations/).",
        },
        {
          type: "definitions",
          items: [
            {
              term: "Test",
              description:
                "Executes an ephemeral check and returns its result without saving history or triggering alerts.",
            },
            {
              term: "Run now",
              description:
                "Publishes a normal job. The result is stored, changes monitor state, and can open or resolve alerts.",
            },
            {
              term: "Scheduled run",
              description:
                "The scheduler claims a due monitor, fans it out to its selected locations when applicable, and advances `next_run_at`.",
            },
          ],
        },
      ],
    },
    {
      id: "verify-and-troubleshoot",
      title: "Verify the installation and troubleshoot startup",
      blocks: [
        {
          type: "code",
          language: "bash",
          title: "Repository checks",
          code:
            "make test\nmake lint\n\ncd web\nnpm run lint\nnpm run build",
        },
        {
          type: "list",
          items: [
            "If the UI loads but API calls fail, verify that `PUBLIC_BASE_URL` names the API origin, not the UI or status-page origin.",
            "If agents or remote workers cannot connect, confirm that the advertised URL is reachable from their network and that proxy/TLS settings preserve the intended scheme.",
            "If an active check is rejected before connecting, review the [destination-safety policy](/docs/security/#ssrf-network-policy) and location-specific CIDR allowlists.",
            "If PostgreSQL authentication fails after older local experiments, the repository-scoped Docker volume may contain incompatible credentials.",
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Database reset destroys local data",
          text:
            "As a last resort for stale local PostgreSQL credentials, `docker compose down -v` removes the repository's Compose volumes so the database can initialize again. This deletes all local Probara data in those volumes.",
        },
      ],
    },
  ],
};
