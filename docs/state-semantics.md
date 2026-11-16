# Monitor State Semantics — Specification

Normative specification for how Probara derives, records, and reports monitor
state. Every rule has a stable ID (`S-XX#`) and a status:

- ✅ **Implemented** — enforced by the cited tests; behavior changes must
  update this file in the same commit.
- 🚧 **Partial** — implemented on some surfaces, violated on others.
- ⏳ **Pending** — target behavior, not yet implemented; the status line names
  the current divergence. Implementation stages refer to the state-semantics
  program plan.

Rule IDs are append-only: never renumber or reuse; deprecate with a note.
Tests cite rule IDs (`shared/monitorstate/spec_test.go`,
`scheduler/internal/ingest/*_integration_test.go`). The analytics/rollup layer
built on top of these semantics is documented in `correctness-notes.md`.

## Model

Monitor status has two orthogonal dimensions:

1. **Observed state** — what evidence says: `up`, `suspect`, `down`,
   `degraded`, `unknown` (`shared/monitorstate/state.go`). Derived by the
   **temporal machine** (`Apply`) per evidence stream, then for
   multi-location monitors by the **spatial rule** (`AggregateLocations`)
   across per-location states.
2. **Administrative status** — operator intent: `enabled` (pause) and
   maintenance windows. Administrative status is never an input to the
   temporal machine; it annotates the timeline and gates dispatch/alerting.

An **evidence stream** is the sequence of check results for one
(monitor, location) pair, or for the monitor itself when location-less.

The **timeline** (⏳) is `monitor_state_intervals`: one row per contiguous
observed-state interval (`state`, `started_at`, `ended_at` NULL while open,
`reason ∈ {result, watchdog_stale, pause, resume, location_change, seed}`),
written in the same transaction as the transition it records. It is the
substrate for time-based availability (S-U rules). Until it exists, the only
transition record is the overwritten `monitors.current_state` +
`last_state_change_at`.

## Diagrams

### Temporal machine (S-T) — per evidence stream

```mermaid
stateDiagram-v2
    direction LR
    [*] --> unknown
    unknown --> up : pass
    unknown --> suspect : fail, CF < N
    unknown --> down : fail, CF >= N
    up --> up : pass
    up --> suspect : fail, CF < N
    up --> down : fail, CF >= N
    suspect --> up : pass (blip)
    suspect --> suspect : fail, CF < N
    suspect --> down : fail, CF >= N
    down --> down : fail
    down --> up : pass
```

N = `consecutive_failures_threshold` (clamped ≥ 1, default 2); CF = consecutive
failures counting this result. Outage bookkeeping rides the `down` boundary
(S-T6); the machine never emits `unknown` or `degraded` (S-T5). Pending S-F2
adds the absence edge: from any state, a stream with no fresh evidence for
`interval × 3` returns to `unknown`, and the first fresh result re-enters
through the machine.

### Spatial rule (S-A) — a decision function, not a state machine

Recomputed from per-location states after every per-location transition;
location-less monitors skip it (their single stream drives
`monitors.current_state` directly).

```mermaid
flowchart TD
    IN["per-location states of selected locations<br/>(⏳ S-A10: fresh evidence only — stale counts as unreported)"] --> Q1{"down >= effective quorum?"}
    Q1 -- yes --> D([down])
    Q1 -- no --> Q2{"down > 0?"}
    Q2 -- yes --> G([degraded])
    Q2 -- no --> Q3{"suspect > 0?"}
    Q3 -- yes --> S([suspect])
    Q3 -- no --> Q4{"up > 0?"}
    Q4 -- yes --> U([up])
    Q4 -- no --> UNK([unknown])
```

Effective quorum = `clamp(location_quorum, 1, selected)` (S-A6); `degraded`
exists only at this level (S-A8).

### Target pipeline (S-U end state)

```mermaid
flowchart LR
    R[check result] --> T["temporal machine<br/>(per evidence stream)"]
    T --> A["quorum aggregation<br/>(fresh locations only ⏳)"]
    A --> CS[monitors.current_state]
    WD(["⏳ watchdog: no fresh evidence<br/>for interval × 3"]) -.-> CS
    CS --> AL[alerter]
    CS -. "on change ⏳" .-> TL[(monitor_state_intervals)]
    TL --> AV["availability % + coverage % ⏳<br/>unknown/paused leave the denominator,<br/>maintenance = planned downtime"]
```

## S-E — Evidence

| ID | Rule | Status |
|----|------|--------|
| S-E1 | Only `result_source='monitor'` rows are evidence. `platform` rows (job expired before a worker observed the target) are recorded as history and excluded from state, uptime, and rollups. | ✅ `scheduler/internal/ingest/persist.go` routes platform rows to `InsertOnly`; test `TestIngestExpiredJobInsertsWithoutState`. Historical caveat: the one-shot state seed in migration 000044 picked each monitor's latest row without filtering `result_source`, so an install upgrading through it with a platform row as its latest could briefly start `down`; self-heals on the first live result — no corrective migration warranted |
| S-E2 | `status ∈ {failure, error}` is failing evidence; `success` is passing evidence. | ✅ `IsFailureStatus`, `state.go`; test `TestIsFailureStatus` |
| S-E3 | Evidence identity is `(job_id, result_source)`. Redelivery of known evidence is a no-op: recorded nowhere, moves nothing. | ✅ unique index (migration 000060) + duplicate short-circuit in `record.go` / `quorum.go`; test `TestIngestIsIdempotentOnRedelivery` |
| S-E4 | Evidence from a location no longer selected for the monitor is history-only: inserted, but never moves per-location or monitor state. | ✅ `quorum.go` membership guard within the ingest transaction (test `TestQuorumStaleLocationResultKeepsHistoryButNoState`); the S-O1 lock order closes the concurrent-mutation window in which an in-flight ingest could re-upsert a just-removed location's state row (test `TestSetLocationsCannotResurrectRemovedLocationState_Integration`) |
| S-E5 | For a monitor with selected locations, location-less evidence is history-only (the inverse of S-E4). | ✅ `Record` checks location binding under the monitor lock (serialized against `SetLocations` per S-O1) and records history without running the machine; test `TestIngestLocationLessResultForLocationBoundMonitorIsHistoryOnly` |

## S-T — Temporal machine (per evidence stream)

`Apply(current, resultIsFailure, threshold)`, pure, `state.go`.

| ID | Rule | Status |
|----|------|--------|
| S-T1 | Passing evidence ⇒ `up`, consecutive failures reset to 0. Recovery is single-sample (a recovery-confirmation knob is a possible future extension, out of scope here). | ✅ `TestApply`, `TestSpecProperty_ApplyInvariants` |
| S-T2 | Failing evidence increments consecutive failures. At `>= threshold` (clamped to ≥ 1) ⇒ `down`; below ⇒ `suspect`. | ✅ same |
| S-T3 | From `down`, failing evidence stays `down` — no re-confirmation window. | ✅ same |
| S-T4 | From `down`, passing evidence ⇒ `up` immediately. | ✅ same |
| S-T5 | The temporal machine never outputs `unknown` or `degraded`. `unknown` comes only from absence (S-F) or resets; `degraded` only from the spatial rule (S-A). | ✅ `TestSpecProperty_ApplyInvariants` |
| S-T6 | An outage opens exactly when a stream enters `down` and closes exactly when it leaves `down` via passing evidence (`OpenedOutage` / `ClosedOutage`). | ✅ `TestApply`, `TestSpecScenario_LocationlessConfirmation` |

## S-A — Spatial rule (multi-location aggregation)

`AggregateLocations(counts, quorum)`, pure, `state.go`; counts assembled in
`scheduler/internal/ingest/quorum.go`.

| ID | Rule | Status |
|----|------|--------|
| S-A1 | `down` when ≥ effective-quorum contributing locations are down. | ✅ `TestAggregateLocations`, `TestSpecScenario_QuorumConfirmationAndRecovery` |
| S-A2 | `degraded` when at least one but fewer than effective-quorum contributing locations are down. | ✅ same |
| S-A3 | `suspect` when none down and any contributing location is mid-confirmation. | ✅ same |
| S-A4 | `up` when none down or suspect and at least one contributing location is up. | ✅ same |
| S-A5 | `unknown` when no location contributes evidence. Absence is never health: locations without evidence contribute to no count (they also can never trip quorum). | ✅ Fresh-only counts make stale and never-reported locations equivalent (S-A10); test `TestWatchdogReaggregatesStaleLocations` |
| S-A6 | Effective quorum = `clamp(location_quorum, 1, Selected)` when `Selected > 0`. | ✅ `TestAggregateLocations`, `TestSpecProperty_AggregateInvariants` |
| S-A7 | A single-location monitor behaves identically to a location-less one. | ✅ `TestSpecScenario_SingleLocationParity` |
| S-A8 | `degraded` is monitor-level only; per-location state is `unknown/up/suspect/down` (DB check, migration 000064). | ✅ schema constraint |
| S-A9 | Monitor-level `consecutive_failures` = max over per-location values (drives display and the suspect fast-recheck). The fast recheck skips side-effecting monitors (SIP INVITE test calls): they keep their interval while suspect, since the interval floor is their call-rate limit. | ✅ `quorum.go`; test `TestQuorumDegradedThenDownThenRecovery`; fast-recheck exemption `nextCheckDelay` (`scheduler.go`), test `TestNextCheckDelay` |
| S-A10 | Only locations with **fresh** evidence (S-F1) contribute to counts. Stale locations are excluded exactly like never-reported ones; a monitor whose contributing set becomes empty is `unknown` (S-A5). | ✅ `monitorstate.LoadFreshLocationCounts` is the single aggregation input, shared by results ingest (`quorum.go`) and the watchdog's re-aggregation pass (stale locations produce no new results, so the watchdog re-derives). Test `TestWatchdogReaggregatesStaleLocations` |

## S-F — Freshness and absence

All pending (Stage 5) except where noted. Precedent: mesh edges already use
read-time staleness at 3 intervals (`api/internal/services/mesh/service.go`,
`staleAfterIntervals`).

| ID | Rule | Status |
|----|------|--------|
| S-F1 | Evidence is fresh for `interval_seconds × 3` (floor 90s) after its **platform receipt time** (server clock at ingest: `monitor_location_state.last_check_at` / `monitors.last_result_at`), then stale. Worker clocks are untrusted for freshness. Per-type grace stays supported where configured (push: `interval + grace_period_seconds`); the agent stale window (previously `2 × interval`, single-shot) folds into the default and now re-fires each window so failures accumulate toward `down`. Agent evidence is the **OTLP export receipt** itself: one accepted export request = one success heartbeat, regardless of how many data points it carried or rejected. | ✅ `monitorstate.FreshnessHorizonSeconds`, and `monitorstate.EvidenceWindowSeconds` for the per-type window incl. push grace (`shared/monitorstate/freshness.go`, `TestEvidenceWindowSeconds`), shared by the absence watchdog and discovery coverage; tested through the watchdog suite. OTLP heartbeat: `api/internal/services/otlp/service.go` |
| S-F2 | A monitor with no fresh evidence transitions to `unknown` (reason `watchdog_stale`). First fresh evidence re-enters through the temporal machine. No data is **never** rendered as healthy. | ✅ `scheduler/internal/scheduler/watchdog.go` (active pass re-checks staleness under the monitor lock); test `TestWatchdogMarksStaleActiveMonitorUnknown` |
| S-F3 | Staleness transitions are ordinary transitions: same per-monitor serialization (S-O1), timeline row, `state_change` event. | ✅ watchdog passes lock, write the interval, and publish through the shared status publisher; interval asserted in `TestWatchdogMarksStaleActiveMonitorUnknown` |
| S-F4 | Exactly one absence detector exists, scheduler-hosted, advisory-locked (`901337403`). It replaces the API-process push/agent stale workers (whose in-process debounce duplicated synthetic failures across API replicas); push/agent synthetic failures now self-debounce via `last_result_at`. | ✅ watchdog heartbeat pass; test `TestWatchdogSynthesizesPushFailure` |
| S-F5 | `unknown` never opens or closes an outage. Alerting on absence (a `no_data` alert kind) is out of scope for this spec — see the notification-trust program. | ✅ by construction: the watchdog writes `unknown` with no outage bookkeeping, and the alerter opens only on `down` / resolves only on `up` (`lifecycle.go` predicates) |
| S-F6 | A check location is **offline** when it is enabled, not deleted, was seen at least once, its `last_seen_at` (platform receipt of its worker heartbeat) is at least 180 s old (`LocationOfflineAfter`, one platform-wide threshold), and it has been enabled for at least as long (`locations.enabled_at`, restarted on a re-enable, migration 000104). It is **disconnected** from 60 s (`LocationConnectedWindow`, the `connected` badge) until then; a never-seen location is neither. Offline is a fleet signal, never a monitor-state input: monitors keep following S-A10 and S-F2, and per-monitor absence stays out of scope (S-F5). Each offline location opens one `location_offline` alert (tenant default channels) and resolves it, with `alerts.resolve_reason` `recovered`, `location_disabled` or `location_deleted` (an operator's resolve is `manual`, and a still-offline location reopens on the next evaluation), once it no longer applies. It opens only for a location seen within 7 days (`LocationOfflineAlertHorizon`), and not while the evaluator cannot trust its freshness source: after its own start until a heartbeat lands later than the start (database clock) or one threshold passes; and, once the newest heartbeat of the installation is older than 60 s, for the locations last heard within 60 s of it when there are at least two (they went quiet together: the heartbeat path or the database, not the sites), until that newest heartbeat is 15 minutes old. A location that went quiet earlier is outside that cluster and alerts. A mesh edge with a disconnected location dispatches nothing while that location is not yet offline or has an open offline alert, because edges into a dead site fail after a few probes, before the site counts as offline; while the location alert is held or beyond the horizon, the edge pages for itself. | ✅ `monitorstate.LocationOfflinePredicate` / `LocationOffline`, `LocationDisconnectedPredicate` / `LocationDisconnected`, `LocationWithinOfflineHorizonPredicate` (`shared/monitorstate/freshness.go`); guard `locationOfflineHold` (`alerter/internal/alerter/location_offline.go`); mesh hold `alertrouting.SuppressedByOfflineLocationPredicate`. Tests `TestSpecLocationOffline`, and in `alerter/internal/alerter/location_offline_integration_test.go` `TestLocationPredicatesMatchGo`, `TestLocationOfflineLifecycle`, `TestLocationOfflineResolvesWhenDisabledOrDeleted`, `TestLocationOfflineManualResolveReopens`, `TestLocationOfflineFleetSilenceStaggered`, `TestLocationOfflineConsecutiveOutagesBothAlert`, `TestLocationOfflineWaitsAfterReEnable`, `TestLocationOfflineHeldAtStartup`, `TestMeshEdgeOfOfflineLocationIsQuiet`, `TestMeshEdgePagesWhileLocationAlertIsHeld` |

## S-O — Ordering and concurrency

| ID | Rule | Status |
|----|------|--------|
| S-O1 | Every writer of monitor state or per-location state locks the monitor row (`FOR UPDATE`) **first**, before touching membership or state rows. | ✅ Ingest paths: `record.go`, `quorum.go` (`TestQuorumConcurrentResultsNoDeadlock`). Location-set mutations: `SetLocations` locks the monitor row before its membership/state writes, location `Delete` locks all affected monitor rows in id order (tests `TestSetLocationsRespectsIngestLockOrder_Integration`, `TestDeleteRespectsIngestLockOrder_Integration`, racing each mutation against an ingest-shaped transaction) |
| S-O2 | Evidence older than the newest applied evidence for the same stream (by `started_at`) inserts into history but does not move state — late results never move state backwards. | ✅ Per-stream evidence watermarks: `monitors.last_result_started_at` (global stream, `record.go`) and `monitor_location_state.last_result_started_at` (`quorum.go`), both updated only when evidence applies. Tests `TestIngestLateResultIsHistoryOnly`, `TestIngestLateLocationResultIsHistoryOnly` |
| S-O3 | The agent stream's `started_at` is the **platform receipt time**, not the reporter's clock: OTLP heartbeats stamp server `NOW()`, so the agent watermark is monotonic by construction and collector clock skew cannot reorder history or wedge the watermark. Agent-clock timestamps live only in `metric_samples.ts` (chart axes), clamped at ingest to ≤ now+5min. The `SyntheticAbsence` watermark bypass (S-F4) remains for watchdog rows; with both clocks server-side its hazard window is strictly narrower than under the retired custom agent. | ✅ `api/internal/services/otlp/service.go` (`heartbeat`); the custom agent's reporter-clocked `/agent/metrics` push is a `410` tombstone since its 2026-11-18 sunset |

## S-P — Pause

Pause is `monitors.enabled = false`; there is no `paused` machine state.

| ID | Rule | Status |
|----|------|--------|
| S-P1 | Pause stops dispatch, absence detection, and alert opening; open availability alerts resolve. | ✅ scheduler due-query filter; `alerter/internal/alerter/lifecycle.go` open/resolve predicates |
| S-P2 | Pausing resets observed state to `unknown` (consecutive failures 0, per-location machinery cleared) and records a `pause` timeline interval; unpausing opens `unknown` (reason `resume`) until first fresh evidence. | ✅ `SetEnabled` (`api/internal/services/monitors/repository.go`), locked per S-O1; the enabled flag no longer rides the dynamic UPDATE. Location-set resets record `location_change` intervals likewise. Test `TestSetEnabledResetsStateAndWritesTimeline_Integration` |
| S-P3 | Paused time is excluded from availability denominators and rendered as paused — never as up, down, or 100%. | 🚧 Zero-check members now render as no-data instead of 100% (S-D1). The interval engine excludes paused time from denominators and SLA reports show it as its own `paused` category (unknown intervals with reason `pause`); sampled surfaces still see paused time only as missing rows. Test `TestIntegrateTimeline_BreakdownSerialAndMaintenanceUnion` |

## S-M — Maintenance

Maintenance windows are interval tables, never duplicated into the state
timeline; consumers join their occurrences (`maintenance_window_occurrences`,
S-M5) at query time via `shared/maintenance/predicate.go`.

| ID | Rule | Status |
|----|------|--------|
| S-M1 | Checks and state transitions continue during maintenance; maintenance is an annotation, not a machine input. | ✅ scheduler due-query has no maintenance predicate |
| S-M2 | Maintenance suppresses alert opening and dispatch. | 🚧 `lifecycle.go` applies the predicate in the candidate SELECT but the alert INSERT does not re-check it: a window created between the two can still open an alert (bounded by one eval tick, default 30s). Close by re-evaluating the predicate inside the INSERT |
| S-M3 | Downtime overlapping a maintenance window is planned downtime: excluded from unavailability by default. | 🚧 Enforced in the interval engine (`shared/analytics/timeline.go`: down ∩ the union of the monitor's maintenance windows, direct or via a group, leaves numerator and denominator — overlapping windows count once) and so in SLA reports; sampled fallback and batch surfaces still count maintenance failures as ordinary downtime, and analytics downtime-period lists (both paths, see S-U1) still list `down` time inside maintenance windows without marking it planned. Tests `TestComputeIntervalAvailability_IntegratesTimeline`, `TestIntegrateTimeline_BreakdownSerialAndMaintenanceUnion` |
| S-M4 | Dependency suppression gates notification dispatch only. A downstream availability alert whose monitor has a down upstream dependency (or whose upstream recovered less than the tenant grace ago) sends nothing while the policy (`monitors.dependency_suppression`, else `tenants.dependency_suppression_enabled`) is on; alert opening, resolution, incidents, status pages and availability accounting are unaffected. | ✅ `alertrouting.SuppressedByDependencyPredicate` in `dispatchOpenAlerts`; grace clock is `alerts.root_cause_cleared_at`, stamped by `annotateOpenAlertRootCauses`. Tests `TestDependencySuppression*` (alerter) and `TestAlertReadPathsReportDependencySuppression` (API twin) |
| S-M5 | A maintenance window is a schedule (a one-off span, or weekly: weekdays + local start time + duration in an IANA timezone, optionally until a local date); what covers monitors is its materialized occurrences, read only through `shared/maintenance` (S-M1–S-M3 hold per occurrence). Wall times follow the zone across DST: a start in a spring-forward gap runs when the gap ends, a repeated hour runs once at its first instance, durations are elapsed time; at most one start per local day, and overlapping occurrences union. Occurrences are materialized 35 days ahead (the API on save, the scheduler rolling forward). Once started an occurrence is history: an edit applies from the moment it is saved (started occurrences keep their start, the running one takes the new schedule's end for now or ends at the edit, future ones are regenerated, coverage never starts before the edit), target changes keep what removed monitors were covered for, and delete ends coverage now while keeping what was covered. End-now and skip-next change one occurrence, never the schedule; a later schedule edit does not revive an occurrence an operator skipped or ended early, but does start one (from the edit) for a slot that simply ran to its end. Two inputs still change past coverage by design: a one-off created with a past start covers from that start, and group membership is read as it is now. Occurrences are never pruned. | ✅ `shared/maintenance` (`Rule.Expand`, `Reschedule`, `Retarget`, `RollForward`, `OccurrenceRangesSQL`); scheduler `maintenance_occurrences.go`. Tests `TestExpand_*`, `TestDensityBound` (`rule_test.go`), `TestRollForward_*`, `TestReschedule_EditsApplyFromNow`, `TestEndNowSkipNextAndRetire`, `TestReschedule_LengtheningAfterANaturalEnd`, `TestMaterialize_OverlappingOccurrencesUnderWay`, `TestCoverageQueriesStayIndexDrivenWithLongHistory`, `TestRecurringMaintenanceWindow` (API), `TestMaintenanceOccurrenceRoll` (scheduler), `TestIntegrateTimeline_RecurringMaintenanceOccurrences`, `TestStatusPageListsRepeatingMaintenanceByOccurrence` |

## S-U — Availability accounting

Partially implemented — see each rule's status. Current sample-count semantics are documented in
`correctness-notes.md`.

| ID | Rule | Status |
|----|------|--------|
| S-U1 | Availability is time-based: integration of the state timeline over the query window, not sample counting. | 🚧 Engine: `IntegrateTimeline` in `shared/analytics/timeline.go`, wired into `GetScopeAnalytics` via `interval_availability.go` (monitor detail headline + dashboard long-range `OverallUptime`) and into SLA reports (`api/internal/services/slas`, timeline only — no sampled fallback). Batch surfaces (status pages, group analytics) and the 24h/1h stitched dashboard scalars still use sampled math. Analytics **downtime periods** are the scope's `down` intervals on both paths — short ranges read the timeline through `Outages` (`loadTimelineDowntime`), long ranges the scheduler's projection of it (`syncDowntimeFromIntervals` → `monitor_downtime_periods`/`_open`, plus pre-timeline rows) — merged across the scope (a group lists the union of its leaf members); degraded, suspect, unknown and paused time is never a period, and failed check rows alone never open one. Tests `TestComputeIntervalAvailability_IntegratesTimeline`, `TestRepository_GetScopeAnalytics_RawDowntimeFollowsStateTimeline`, `TestDowntimeProjectionMatchesRawAnalyticsPath` |
| S-U2 | Denominator = window − `unknown` − paused (− planned downtime per S-M3). **Coverage** = denominator ÷ window, always reported alongside availability. | ✅ In the interval engine (paused time is recorded as `unknown` intervals, so one exclusion covers both); coverage returned as `coverage_pct` wherever the method is `interval`. Same test |
| S-U3 | `degraded` counts as available by default; `suspect` (mid-confirmation) likewise. The knob is per SLA (`degraded_counts_as_down`), nowhere else. | ✅ interval engine (`TimelineOpts.DegradedCountsAsDown`). Test `TestIntegrateTimeline_DegradedKnobAndLateTimeline` |
| S-U4 | The timeline source of truth is `monitor_state_intervals`, written in the transition's transaction. Timestamps are monotonic per monitor: each interval starts exactly where the previous one ended, even when the writing transaction began (and so pinned its `NOW()`) before the transaction it queued behind on the S-O1 lock. | 🚧 Written everywhere a monitor-level transition happens (global machine, quorum aggregate, pause/resume, location changes, creation, migration 000082 seed) with exactly one open interval per monitor — except **group monitors**, whose alerter-SQL state derivation records no transitions (excluded from the timeline until unified). `RecordIntervalTx` (and its bulk twin `RecordIntervalsTx`, used by location deletion) close and open in one statement, clamping the close at the interval's own start (a transition that lost the lock race by less than its wait records a zero-length interval rather than failing the `ended_at >= started_at` check). Availability math consumes the timeline in Stage 6. Tests `TestIngestWritesStateIntervals`, `TestRecordIntervalTxEarlierTransactionLosingTheLockStaysMonotonic` + the S-P2/S-O2 suites |
| S-U5 | Windows predating the timeline cutover use sampled math, labeled `method: "sampled"`; never silently mixed. | ✅ A scope is interval-covered only when EVERY monitor's timeline reaches the window start; otherwise the sampled rate stands in under `method: "sampled"` (group monitors always fall back — no timeline). SLA reports never fall back: time before a monitor's first interval is reported as untracked (lost coverage) with a note. Tests `TestGetScopeAnalytics_LabelsMethod`, `TestIntegrateTimeline_DegradedKnobAndLateTimeline` |
| S-U6 | A serial composite (SLA `aggregation: serial`) is down whenever any member is in unplanned down; otherwise excluded whenever any member is planned-down, paused, unknown or untracked; otherwise available. A mean composite is the unweighted mean of member availabilities (the D2 convention). Groups contribute their leaf members, never themselves. | ✅ `TimelineResult.Serial` (multirange unions in one query), `summarize` in `api/internal/services/slas/report.go`. Tests `TestIntegrateTimeline_BreakdownSerialAndMaintenanceUnion`, `TestSLAService` |
| S-U7 | Reporting periods are calendar weeks (ISO, Monday start), months or quarters in an IANA timezone; a running period is reported to date. Day buckets follow the local calendar (a DST day is 23 or 25 hours). | ✅ `shared/analytics/period.go`. Tests `TestPeriods_DSTMonthsFollowTheLocalCalendar`, `TestPeriods_KeysAndStepping` |
| S-U8 | **Error-budget burn.** The **burn rate** over a window = (1 − A) ÷ (1 − target), A the window's availability (S-U1, S-U6): 1 spends the budget exactly at the allowed pace, 2 twice as fast, 0 not at all. Windows: the last **1 h** and **24 h** (running periods only), ending at the report's effective end and clipped to the period start, and the **period to date**. No data ⇒ no burn rate (S-D1); a target that leaves no budget has none either. The **burn-down** is, after each local day (S-U7), the full period's budget left: 100 × (1 − consumed to date ÷ ((1 − target) × the whole period)), consumed = (1 − A) × eligible time from the period start through that day (exact for both aggregations; the last day consumes exactly the headline budget), null until something was observed. **Exhaustion**: `exhausted` once consumption reaches the full period's budget; else, for a running period, `projected` at the instant the remaining budget runs out at the last 24 h's consumption per wall-clock second, when that falls before the period end, otherwise `not_projected`; a closed period with budget left is `not_projected`; `no_data` without data in the period (or, running, in the last 24 h). Issued reports computed before burn existed carry none of these and render unchanged. | ✅ `burnWindows`/`buildBurn`/`projectExhaustion`/`burnDown` in `api/internal/services/slas/burn.go`, on the report's one `IntegrateTimeline` call. Tests `TestBurnRate`, `TestBurnDownIsCumulativeAndEndsAtTheHeadline`, `TestBurnDownEmptyAndZeroBudget`, `TestBurnWindowsClipToThePeriod`, `TestProjectExhaustion`, `TestBuildBurnRunningPeriod`, `TestReportWithBurnExports`, `TestLegacyIssuedReportRendersUnchanged`, `TestSLAService` |

## S-G — Group monitors

A group has no checks of its own; its state is derived from its leaf members.

| ID | Rule | Status |
|----|------|--------|
| S-G1 | A group's members are its enabled, non-deleted, non-group descendants, deduplicated across nested membership paths; a disabled monitor or sub-group hides everything beneath it. The input is each member's monitor-level `current_state` (so quorum, thresholds and absence are already applied) — never a member's check rows. | ✅ `LoadGroupSummaries` / `GroupSummary` in `shared/monitorstate/group.go`; the alerter's set-based refresh aggregates the same members. Test `TestGroupAlertStateSQLMatchesSummary` |
| S-G2 | **Alert view** (what the alerter stores in the group's `current_state` and alerts on): `down` if any member is down; `up` if there is at least one member and every member is up, suspect or degraded; otherwise `unknown` (including an empty group). A group is never `degraded` in this view. | ✅ `GroupSummary.AlertState` and its SQL twin `GroupAlertStateSQL` (used by `refreshGroupStates`). Tests `TestGroupSummaryViews`, `TestGroupAlertStateSQLMatchesSummary`, `TestGroupStatesRefreshNestedLeavesInOneTick` |
| S-G3 | **Display view** (operator UI group status, batch results, public status pages): members with no verdict are left out while at least one member has one; all of those down is `down`, any of them down or degraded is `degraded`, otherwise `up`; no member with a verdict is `unknown`. A partial outage therefore reads `degraded` while the alert view (S-G2) is already `down`. | ✅ `GroupSummary.DisplayState` / `DisplayCheckStatus`; `api/internal/services/groups` (`GetGroupStatus`), `api/internal/services/results` (batch), `status-page` (`GetGroupAggregatedStatus`). Tests `TestGroupSummaryViews`, `TestGroupStatusFollowsMemberStatesNotCheckRows`, `TestService_GetGroupStatus_EmptyIsUnknownAndMixedIsDegraded`, `TestService_GetMonitorResultsBatch_MatchesPerMonitorResults` |

Still outside the rules: group uptime, latency and history on display
surfaces are aggregated from member check rows (sampled), and groups write no
state intervals (S-U4).

## S-D — Display conventions

| ID | Rule | Status |
|----|------|--------|
| S-D1 | An empty denominator renders as no-data (“—” / grey), never as 0% and never as 100%. | ✅ Analytics summaries carry `has_data` (`shared/analytics`); dashboard `overall_uptime`, group/member uptime, and sparkline buckets are null on no data (`api/internal/services/dashboard`); series buckets pair the value with `has_data`/`total_checks`; status pages keep the −1 sentinel, and a rollup row with zero checks now renders as no-data, not a red 0% day. Tests: `TestAggregateGroups_NoDataIsNotHundredPercent`, `TestResampleTo_PreservesNoDataGaps`, `web/lib/dashboard-view-model.test.ts` (no-data uptime tile) |
| S-D2 | `unknown` is a visible, distinct category on every surface — never folded into up/ok. | 🚧 Status pages render it; dashboard ops summary skips unknown monitors from status counts |

## Conformance

Group alert state is derived from enabled, nondeleted leaf descendants,
deduplicated across nested membership paths. An empty group is `unknown` and
its prior availability alert resolves. Failure and recovery reach every
ancestor in one alerter evaluation; stored intermediate group states are not
inputs. `TestGroupStatesRefreshNestedLeavesInOneTick` and
`TestGroupStatesClearEmptyGroupOutages` cover nesting, cycles, and removal,
pause, or deletion of the final participating member. This does not add group
state intervals or change the separate sampled display/availability surfaces
described above.

Pure-layer rules are enforced by `shared/monitorstate/spec_test.go` (golden
scenarios + fixed-seed property tests, each citing rule IDs) together with the
pre-existing `state_test.go` / `aggregate_test.go`. Transactional rules
(S-E1, S-E3, S-E4, S-O1) are enforced by the testcontainers suites in
`scheduler/internal/ingest/` plus the lock-order race tests in
`api/internal/services/monitors/` and `api/internal/services/locations/`.
Pending rules gain their enforcing test in the stage that implements them; a
stage is not done while its rules still read ⏳.

## Change discipline

- Behavior changes to anything specified here update this file in the same
  commit (rule status, or a new rule) — same policy as `CLAUDE.md` for docs.
- New aggregation surfaces (groups, mesh, future roll-ups) must cite which
  S-A/S-F rules they satisfy rather than inventing parallel semantics. Group
  state has one derivation with two views (S-G); a new surface showing a
  group reads `monitorstate.LoadGroupSummaries`, never member check rows.
