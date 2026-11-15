package alertrouting

// Relapse reopen (flap suppression), off unless a workspace sets
// tenants.alert_reopen_cooldown_minutes. When the subject of an alert the
// alerter resolved on its own (auto_resolved) fails again less than that long
// after the resolve, that alert is reopened instead of a new one opening:
// back to active, relapse_count + 1, reopened_at = now, triggered_at kept,
// and the acknowledgement and snooze of the episode that recovered cleared
// (first_acknowledged_at stays, for SLA reports). "Same subject" is the
// alert's identity: monitor and kind, plus the metric series for host_metric,
// the location pair for mesh_edge and the location for location_offline. Only
// the subject's latest alert can reopen, and a manually resolved alert never
// does. The alerter performs the reopen (alerter/internal/alerter/relapse.go).
//
// Notifications follow the reopen like a new paging episode:
//
//   - A route that heard the earlier recovery is told once that the alert
//     relapsed: the EventRelapsed claim, sent to the channel as a "created"
//     page carrying the relapse count (present words it as a relapse).
//   - A route that never heard that recovery (its resolve was still pending,
//     or was dropped by the reopen) still counts as paged: it gets reminders
//     and the relapse's own resolve, but no relapse notice.
//   - Escalation delays and reminders count from the reopen
//     (DispatchResumedAtExpr includes reopened_at), and the acknowledge/snooze
//     mute holds the relapse notice like any page (MuteHolds).

// EventRelapsed is the claim event of a relapse notice in
// alert_notification_states.last_event_type: it moves a route's row from
// 'resolved' to 'relapsed' once per reopen. Plugins never see it; the alerter
// sends the notice as a "created" event.
const EventRelapsed = "relapsed"

// MaxReopenCooldownMinutes bounds tenants.alert_reopen_cooldown_minutes (one
// day; the column's CHECK mirrors it). 0 turns relapse reopen off.
const MaxReopenCooldownMinutes = 1440
