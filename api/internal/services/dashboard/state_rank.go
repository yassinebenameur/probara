package dashboard

import (
	"fmt"
	"strings"
)

// currentStateRanks orders monitors by their persisted state
// (monitors.current_state): down first, then degraded (suspect reads as
// degraded, as the web's problemMonitorStatus does), then unknown, then up.
// It mirrors STATUS_RANK in web/lib/monitor-utils.ts, which the dashboard
// re-sorts Needs attention by, so a server-side cut keeps the rows the client
// would put on top. currentStateRankSQL is built from the same table.
var currentStateRanks = []struct {
	state string
	rank  int
}{
	{"down", 0},
	{"degraded", 1},
	{"suspect", 1},
	{"unknown", 2},
	{"up", 3},
}

// currentStateRankDefault ranks a state the table does not know with "up":
// it never jumps ahead of a real problem.
const currentStateRankDefault = 3

func currentStateRank(state string) int {
	for _, entry := range currentStateRanks {
		if entry.state == state {
			return entry.rank
		}
	}
	return currentStateRankDefault
}

// currentStateRankSQL is currentStateRank as a SQL expression over column.
func currentStateRankSQL(column string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CASE %s", column)
	for _, entry := range currentStateRanks {
		fmt.Fprintf(&b, " WHEN '%s' THEN %d", entry.state, entry.rank)
	}
	fmt.Fprintf(&b, " ELSE %d END", currentStateRankDefault)
	return b.String()
}

// currentStateNeedsAttention reports whether a monitor in this state needs
// attention NOW: down or degraded (suspect included). Failed checks earlier
// in the range do not count; the range's uptime already reflects them.
func currentStateNeedsAttention(state string) bool {
	return currentStateRank(state) <= 1
}
