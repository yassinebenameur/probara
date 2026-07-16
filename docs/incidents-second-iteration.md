# Incident Second Iteration

## Goal
The second iteration should make incidents faster to operate and easier to integrate without changing the core model introduced in the MVP.

## What to add next
- More creation entry points:
  - create incident from alert views
  - create incident from monitor detail
- External incident API and integration-friendly create and update flows.
- Owner assignment.
- Severity with a small fixed enum, not a tenant-customizable taxonomy.
- Better public communication tools:
  - richer public update composer
  - cleaner public incident history presentation
- Carefully bounded recovery automation:
  - optional auto-move to `monitoring` when all linked alerts recover
  - optional auto-resolve only after a healthy cooldown
- Better operator controls:
  - manual merge of incidents
  - better suggestions for attaching related alerts to an existing incident
- Subscriber notifications only if status-page subscriber infrastructure is added first.

## What stays stable
- Incidents remain distinct from alerts.
- Alerts remain the machine detection and responder-notification system.
- Incidents remain the human-managed coordination and customer-communication system.
- The core lifecycle stays:
  - `investigating`
  - `identified`
  - `monitoring`
  - `resolved`
- `acknowledged` stays on alerts, not incidents.
- Status-page publication remains explicit and operator-controlled.

## What still should not ship in iteration 2
- custom incident state machines
- approval workflows
- full on-call scheduling
- Slack-native war rooms or chat-based collaboration
- root-cause assistants or AI summaries
- postmortem generator
- deep ITSM or ticketing suite behavior
- service dependency graph modeling
- advanced automation playbooks
- SLO-aware incident logic
- cross-tenant shared incidents

## Why this is the right second step
Iteration 2 should increase operator leverage, not widen the product into enterprise process software. Owner, severity, better entry points, integration support, and bounded recovery automation make incidents meaningfully better without forcing a redesign of the MVP model.

## Implementation note
Keep the same split if work is parallelized:
- backend and domain work for automation, integrations, and contract changes
- frontend and status-page work for operator flows and public communication improvements
