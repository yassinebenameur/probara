package models

// PassiveMonitorTypes are the monitor types no worker executes: a group rolls
// up its members, and push and agent monitors are fed by heartbeats the API
// receives. The scheduler never dispatches them, run-now refuses them, the
// worker skips any stray job for one, and the stale watchdog leaves them to
// the heartbeat pass. web/app/monitors/[id]/page.tsx (PASSIVE_TYPES) mirrors
// this list, and so does the monitors_timeout_check SQL constraint (migration
// 000099): only these types skip its 0 < timeout < interval rule, so adding a
// passive type means a migration rewriting it; active types need none.
var PassiveMonitorTypes = []string{"group", "push", "agent"}

// IsPassiveMonitorType reports whether monitorType is in PassiveMonitorTypes.
func IsPassiveMonitorType(monitorType string) bool {
	for _, t := range PassiveMonitorTypes {
		if t == monitorType {
			return true
		}
	}
	return false
}
