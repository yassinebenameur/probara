package alerter

import "fmt"

// recoveringPredicate is TRUE when the monitor aliased by alias is down but
// already passing again, counting checks toward confirming its recovery
// (S-T7, docs/state-semantics.md). Such a monitor never opens a new
// availability alert: one opened before stays open until the confirmation,
// but when the outage was never announced — it went down inside a maintenance
// window that has since ended — a page now would announce an outage the
// target has already left, and resolve it moments later.
//
// A group is recovering when it is down only through members that are: its
// enabled, non-deleted leaf descendants (S-G1), every one of the down ones
// counting passes. Its own row carries no count (no checks of its own).
func recoveringPredicate(alias string) string {
	return fmt.Sprintf(`(%[1]s.current_state = 'down' AND CASE WHEN %[1]s.type <> 'group'
		THEN %[1]s.consecutive_successes > 0
		ELSE NOT EXISTS (
			WITH RECURSIVE leaves AS (
				SELECT child.id, child.type
				FROM monitor_groups mg
				JOIN monitors child ON child.id = mg.monitor_id AND child.tenant_id = %[1]s.tenant_id
					AND child.deleted_at IS NULL AND child.enabled
				WHERE mg.group_id = %[1]s.id
				UNION
				SELECT child.id, child.type
				FROM leaves l
				JOIN monitor_groups mg ON mg.group_id = l.id AND l.type = 'group'
				JOIN monitors child ON child.id = mg.monitor_id AND child.tenant_id = %[1]s.tenant_id
					AND child.deleted_at IS NULL AND child.enabled
			)
			SELECT 1 FROM leaves l JOIN monitors leaf ON leaf.id = l.id
			WHERE l.type <> 'group' AND leaf.current_state = 'down' AND leaf.consecutive_successes = 0
		) END)`, alias)
}
