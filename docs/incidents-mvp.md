# Incident MVP

## Product stance
An incident in Probara is a human-owned operational and communication object. Alerts remain machine detections and notification triggers. Incidents sit above alerts and give teams a single place to track customer impact, link related alerts and monitors, and decide what should be communicated publicly.

## What ships in v1
- First-class `incident` object in the API and database, tenant-scoped and distinct from alerts.
- Two creation modes only:
  - manual creation from a dedicated `Incidents` area
  - automatic creation from alert events when an alert policy explicitly opts in
- Minimal state model:
  - `investigating`
  - `identified`
  - `monitoring`
  - `resolved`
- Manual state transitions and manual resolution only.
- Linking in v1:
  - alerts
  - monitors
  - group monitors through the existing monitor model
  - synthetic checks through the existing monitor model
- Manual status-page publication:
  - default is internal-only
  - user chooses which status page or pages to publish to
  - user chooses affected components from monitors already present on that status page
  - empty component selection is allowed for platform-wide issues
- Incident timeline with:
  - system events
  - internal notes
  - public updates
- Minimum admin UI:
  - incidents list
  - incident detail
  - create dialog
  - state update controls
  - attach and detach alerts
  - attach and detach monitors
  - publish and unpublish to status pages
  - timeline view and note composer

## Core product decisions
- Auto-created incidents are included in MVP.
- Auto-create is driven from alert events, not directly from raw monitor failures.
- Auto-create is controlled by one alert-policy flag: `create_incident_on_fire`.
- `acknowledged` is not an incident state.
- `detected` is not needed; auto incidents start in `investigating`.
- `closed` is not part of v1.
- Manual incidents may exist without any linked monitor.
- Multiple alerts may attach to a single incident, but auto-grouping is intentionally narrow:
  - reuse one open auto-created incident only when `(tenant_id, monitor_id, alert_policy_id)` matches
  - do not auto-group across different monitors or policies
- Auto-created incidents are not auto-published.
- Incidents are not auto-resolved in v1.
- Incidents do not send their own internal notifications in v1; alert channels remain the responder-notification mechanism.
- Public status-page subscriber notifications are out of scope in v1.

## Lifecycle and automation rules
- New manual incidents start in `investigating`.
- New auto-created incidents also start in `investigating`.
- A human moves incidents to `identified`, `monitoring`, and `resolved`.
- When all linked alerts recover, the system records that as a timeline event, but it does not change incident state automatically.
- Recurrence after resolution creates a new incident instead of reopening the old one.

## Status-page behavior
- Status-page publication is a deliberate action from incident detail.
- Public title, summary, and updates are global to the incident, not different per status page.
- An incident can be published to multiple status pages, but its public narrative stays the same everywhere.
- Status pages render published incidents from the incident model instead of placeholder content.
- Live updates should reuse the existing status-page refresh path, extended so incident-only changes can target a `status_page_id` and not only a `monitor_id`.

## Minimum backend shape
- `incidents`
- `incident_alerts`
- `incident_monitors`
- `incident_timeline_entries`
- `incident_status_page_publications`
- `incident_status_page_monitors`
- add `create_incident_on_fire boolean not null default false` to `alert_policies`

## Minimum API surface
- `GET /v1/incidents`
- `POST /v1/incidents`
- `GET /v1/incidents/{id}`
- `PATCH /v1/incidents/{id}`
- `POST /v1/incidents/{id}/state`
- `POST /v1/incidents/{id}/alerts`
- `DELETE /v1/incidents/{id}/alerts/{alertId}`
- `POST /v1/incidents/{id}/monitors`
- `DELETE /v1/incidents/{id}/monitors/{monitorId}`
- `POST /v1/incidents/{id}/timeline`
- `PUT /v1/incidents/{id}/status-pages/{statusPageId}`
- `DELETE /v1/incidents/{id}/status-pages/{statusPageId}`

## Explicit non-goals
- cross-monitor auto-grouping
- owner or incident commander
- severity or impact levels
- external incident API and inbound integrations
- incident-specific internal notifications
- subscriber email or SMS notifications
- auto-resolve or cooldown logic
- custom workflows or tenant-specific state machines
- merge and split operations
- AI summaries, postmortems, retrospectives, or ITSM-style features

## Why this cut is right
This is the smallest incident feature that feels commercially real. It includes the important bridge from alerts into incidents, but it keeps the customer-facing parts safe by making publication and resolution manual. It gives Probara a clean incident object model without turning v1 into a workflow engine.

## Implementation note
If implementation is parallelized, split it into exactly two workstreams:
- backend and domain work: schema, API, alert-driven creation, status-page query contract
- frontend and status-page work: incidents UI, publication UI, public rendering
