package alertrouting

// Relapse reopen (flap suppression), off unless a workspace sets
// tenants.alert_reopen_cooldown_minutes. When the subject of an alert the
// alerter resolved because its condition cleared (auto_resolved) fails again
// less than that long after the resolve, that alert is reopened instead of a
// new one opening: back to active, relapse_count + 1, reopened_at = now,
// triggered_at kept, and the acknowledgement and snooze of the episode that
// recovered cleared (first_acknowledged_at stays, for SLA reports). "Same
// subject" is the alert's identity: monitor and kind, plus the metric series
// for host_metric, the location pair for mesh_edge and the location for
// location_offline. Only the subject's latest alert can reopen. A manual
// resolve never makes an alert reopenable, and neither does the alerter
// closing one because its subject left evaluation (paused, disabled or
// deleted monitor, removed rule, stale readings, vanished mesh edge, emptied
// group): the subject coming back then opens a new alert. The alerter
// performs the reopen (alerter/internal/alerter/relapse.go).
//
// Each reopen starts a paging episode, numbered by relapse_count. Every
// route records the episode of its last page (alert_notification_states.
// paged_relapse), and notifications follow from it:
//
//   - A route last paged in an earlier episode is told once that the alert
//     relapsed: the EventRelapsed claim, sent to the channel as a "created"
//     event flagged RelapseNotice (present words only that one as a
//     relapse). This holds whether or not the route heard the recovery the
//     reopen interrupted: a paging provider whose incident is still open,
//     maybe acknowledged, would page nobody on a reminder.
//   - Paging providers key their incident per episode, so the notice closes
//     the route's previous incident and opens a new one, and a late resolve
//     closes only the incident it was for.
//   - A route first paged during a relapse gets an ordinary page.
//   - Escalation delays and reminders count from the reopen
//     (DispatchResumedAtExpr includes reopened_at), and the acknowledge/snooze
//     mute holds the relapse notice like any page (MuteHolds).
//   - A resolve not yet delivered when the alert reopens is dropped while
//     the alert stays open (paging providers excepted); once the alert has
//     recovered again it is delivered, whatever episode it was queued in.

// EventRelapsed is the claim event of a relapse notice in
// alert_notification_states.last_event_type: it moves a route last paged in
// an earlier episode to this one, once per reopen. Plugins never see it; the
// alerter sends the notice as a "created" event.
const EventRelapsed = "relapsed"

// MaxReopenCooldownMinutes bounds tenants.alert_reopen_cooldown_minutes (one
// day; the column's CHECK mirrors it). 0 turns relapse reopen off.
const MaxReopenCooldownMinutes = 1440
