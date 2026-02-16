# Architecture Documentation

## High-Level Architecture

The Probara monitoring platform follows a microservices architecture with clear separation of concerns. All services communicate via:

- **HTTP/gRPC APIs**: For synchronous communication (API service)
- **NATS JetStream**: For asynchronous job distribution
- **PostgreSQL**: Shared database for metadata, monitors, users, alert rules, and results

```
┌─────────────┐
│   Clients   │
└──────┬──────┘
       │
       ▼
┌─────────────────────────────────────────────────────────┐
│                    API Service                           │
│  (HTTP Server, CRUD Operations, Authentication)          │
└──────┬──────────────────────────────┬────────────────────┘
       │                              │
       │                              │
       ▼                              ▼
┌──────────────┐              ┌──────────────┐
│  PostgreSQL  │              │ NATS JetStream│
│   (Shared)   │              │    (Queue)    │
└──────┬───────┘              └──────┬───────┘
       │                              │
       │                              │
       ▼                              ▼
┌──────────────┐              ┌──────────────┐
│  Scheduler   │              │    Worker    │
│   Service    │──────────────▶│   Service    │
│              │  (enqueues)  │              │
└──────────────┘              └──────┬────────┘
                                     │
                                     │ (results)
                                     ▼
                            ┌──────────────┐
                            │   Alerter    │
                            │   Service    │
                            └──────────────┘
                                     │
                                     │ (notifications)
                                     ▼
                            ┌──────────────┐
                            │ Status Page  │
                            │   Service    │
                            └──────────────┘
```

## Component Descriptions

### API Service

- **Purpose**: Main entry point for all CRUD operations
- **Responsibilities**:
  - Monitor management (create, read, update, delete)
  - User and tenant management
  - Alert rule configuration
  - Status page configuration
  - Authentication and authorization
- **Interfaces**: HTTP REST API
- **Dependencies**: PostgreSQL, NATS (for async operations)

### Scheduler Service

- **Purpose**: Schedules monitor checks based on intervals
- **Responsibilities**:
  - Read monitor definitions from database
  - Calculate next run times
  - Enqueue check jobs to NATS JetStream
  - Leader election (only one active scheduler)
- **Interfaces**: Minimal HTTP (health/metrics only)
- **Dependencies**: PostgreSQL, NATS JetStream

### Worker Service

- **Purpose**: Executes HTTP/API checks
- **Responsibilities**:
  - Consume jobs from NATS JetStream
  - Execute HTTP requests to target endpoints
  - Record results to database
  - Expose Prometheus metrics
- **Interfaces**: Minimal HTTP (health/metrics only)
- **Dependencies**: NATS JetStream, PostgreSQL
- **Scalability**: Horizontally scalable (multiple replicas)

### Alerter Service

- **Purpose**: Evaluates alert rules and sends notifications
- **Responsibilities**:
  - Read alert rules from database
  - Evaluate rules against recent check results
  - Send notifications (email, Slack, Discord, webhooks)
  - Maintain alert state and deduplication
- **Interfaces**: Minimal HTTP (health/metrics only)
- **Dependencies**: PostgreSQL, NATS (for async notifications)

### Status Page Service

- **Purpose**: Public-facing status pages
- **Responsibilities**:
  - Render uptime statistics
  - Display recent incident history
  - Support custom branding (names, colors, logos)
- **Interfaces**: HTTP (public-facing)
- **Dependencies**: PostgreSQL (read-only)

## Communication Patterns

### Synchronous Communication

- **API → Database**: Direct SQL queries for CRUD operations
- **Status Page → Database**: Read-only queries for public data

### Asynchronous Communication

- **Scheduler → Worker**: Jobs published to NATS JetStream stream `jobs.checks`
- **Worker → Alerter**: Results may trigger alert evaluation (via database or queue)
- **Alerter → External**: Notifications sent via email, Slack, webhooks, etc.

### Job Flow

1. Scheduler reads monitors from database
2. Scheduler enqueues check jobs to `jobs.checks` stream
3. Worker consumes jobs from `jobs.checks`
4. Worker executes HTTP check
5. Worker writes result to database
6. Alerter periodically evaluates alert rules against results
7. Alerter sends notifications if conditions are met

## Data Flow

### Check Execution Flow

```
Monitor Definition (DB)
    ↓
Scheduler (reads, calculates next_run_at)
    ↓
NATS JetStream (job.checks stream)
    ↓
Worker (consumes, executes HTTP request)
    ↓
Result (written to DB)
    ↓
Alerter (evaluates rules, sends notifications)
```

### Status Page Flow

```
Public Request
    ↓
Status Page Service
    ↓
Database (read-only queries)
    ↓
Rendered HTML/JSON Response
```

## Shared Components

### Configuration (`shared/config`)

- Environment variable parsing
- Service-specific config structs
- Validation and fail-fast on missing required vars

### Logging (`shared/logger`)

- Structured JSON logging (logrus)
- Standard fields: timestamp, level, service, message
- Context helpers for request ID, job ID, tenant ID

### Database (`shared/db`)

- PostgreSQL connection pool management
- Health check functionality
- Generic interface for future extension

### Queue (`shared/queue`)

- NATS JetStream client wrapper
- Generic publish/consume interface
- Subject naming conventions
- At-least-once delivery support
- DLQ placeholder

### Models (`shared/models`)

- Generic job envelope (ID, tenant, type, version, payload, deadline)
- Generic result envelope (ID, job ID, status, timestamps, version)
- Versionable structs for future compatibility

### Metrics (`shared/metrics`)

- Prometheus registry setup
- Standard Go runtime metrics
- Generic service-level counters
- HTTP handler for /metrics endpoint

## Deployment Architecture

### Kubernetes Deployment

- **Deployments**: One per service (API, Scheduler, Worker, Alerter, Status Page)
- **Services**: ClusterIP for API and Status Page
- **Ingress**: Optional, for external access
- **HPA**: HorizontalPodAutoscaler for Worker service
- **ConfigMap/Secrets**: Environment-based configuration

### Scaling Strategy

- **API**: Horizontally scalable (multiple replicas)
- **Scheduler**: Single active (leader election), but can deploy multiple for HA
- **Worker**: Horizontally scalable (multiple replicas, HPA enabled)
- **Alerter**: Horizontally scalable (multiple replicas)
- **Status Page**: Horizontally scalable (multiple replicas)

## Observability

### Logging

- Structured JSON logs to stdout/stderr
- Standard fields: timestamp, level, service, message
- Context propagation: request ID, job ID, tenant ID

### Metrics

- Prometheus metrics endpoint on `/metrics`
- Standard Go runtime metrics
- Service-specific counters (to be added in future modules)

### Health Checks

- `/healthz`: Liveness probe (process is alive)
- `/readyz`: Readiness probe (dependencies are available)

## Security Considerations

- Non-root user in containers
- Read-only root filesystem (where possible)
- Security context with dropped capabilities
- Secrets management via Kubernetes Secrets
- Network policies (to be configured in production)

## Future Module Roadmap

### Module 2: Database Schema
- PostgreSQL migrations
- Monitor, tenant, user, alert rule tables
- Result storage schema

### Module 3: Monitor CRUD
- API endpoints for monitor management
- Validation and business logic
- Database operations

### Module 4: Scheduling Logic
- Leader election implementation
- Next run calculation
- Job enqueueing

### Module 5: Check Execution
- HTTP client implementation
- Timeout and retry logic
- Result recording

### Module 6: Alerting
- Rule evaluation engine
- Notification channels (email, Slack, etc.)
- Alert state management

### Module 7: Status Pages
- Page rendering
- Uptime calculation
- Incident history

### Module 8: Authentication
- JWT-based authentication
- RBAC implementation
- Tenant isolation

### Module 9: Frontend
- React + TypeScript UI
- Monitor dashboard
- Status page editor

## Single Module Architecture Rationale

The project uses a single Go module at the repository root with all services as subpackages. This approach:

- Simplifies dependency management
- Enables code sharing without version conflicts
- Reduces build complexity
- Facilitates refactoring across services
- Aligns with Go best practices for monorepos

All shared code lives in `shared/` and is imported directly:
```go
import "github.com/yassinebenameur/probara/shared/config"
```

