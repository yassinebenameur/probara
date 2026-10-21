package alerts

import "fmt"

// ListSort orders ListAlerts. The zero value is the API default: newest
// triggered first.
type ListSort struct {
	// Column is a key of alertSortColumns ("" = SortTriggeredAt).
	Column string
	// Desc reverses the column's order.
	Desc bool
}

// Sortable alert list columns (the `sort` query parameter).
const (
	SortTriggeredAt = "triggered_at"
	SortStatus      = "status"
	SortMonitor     = "monitor"
	SortDuration    = "duration"
)

// alertSortColumns whitelists the sortable columns, mapping each to the SQL
// expression it orders by over alertDetailSelect's aliases. Nothing from the
// request ever reaches the ORDER BY except through this table.
//
// Every order ends in a.id, so equal keys page deterministically (OFFSET
// paging would otherwise repeat or skip rows across pages). triggered_at
// sorts are served by the triggered_at indexes; the computed columns
// (status rank, subject, duration) sort the tenant's filtered set, which the
// status/since filters keep bounded.
var alertSortColumns = map[string]string{
	SortTriggeredAt: "a.triggered_at",
	// Lifecycle order: active, acknowledged, resolved.
	SortStatus: "CASE a.status WHEN 'active' THEN 0 WHEN 'acknowledged' THEN 1 ELSE 2 END",
	// The alert's subject as the list shows it: the monitor, or a mesh
	// edge's "source → target".
	SortMonitor: "lower(COALESCE(m.name, sl.name || ' → ' || tl.name))",
	// An open alert's duration runs to now.
	SortDuration: "(COALESCE(a.resolved_at, NOW()) - a.triggered_at)",
}

// ParseListSort validates the `sort` / `dir` query parameters. An empty
// column is the default (triggered_at); an empty dir is the column's natural
// direction (newest and longest first, status and subject ascending).
func ParseListSort(column, dir string) (ListSort, error) {
	if column == "" {
		column = SortTriggeredAt
	}
	if _, ok := alertSortColumns[column]; !ok {
		return ListSort{}, fmt.Errorf("invalid sort %q: expected one of triggered_at, status, monitor, duration", column)
	}
	switch dir {
	case "":
		return ListSort{Column: column, Desc: column == SortTriggeredAt || column == SortDuration}, nil
	case "asc":
		return ListSort{Column: column}, nil
	case "desc":
		return ListSort{Column: column, Desc: true}, nil
	default:
		return ListSort{}, fmt.Errorf("invalid dir %q: expected asc or desc", dir)
	}
}

// orderBy renders the ORDER BY list for a sort. Ties under a non-time column
// read newest first (the queue order), then by id. NULLS LAST keeps a missing
// subject (a deleted location) at the end in both directions.
func (s ListSort) orderBy() string {
	column := s.Column
	if column == "" {
		column = SortTriggeredAt
		s.Desc = true
	}
	expr, ok := alertSortColumns[column]
	if !ok {
		expr, s.Desc, column = alertSortColumns[SortTriggeredAt], true, SortTriggeredAt
	}
	dir := "ASC"
	if s.Desc {
		dir = "DESC"
	}
	if column == SortTriggeredAt {
		return fmt.Sprintf("a.triggered_at %s, a.id %s", dir, dir)
	}
	return fmt.Sprintf("%s %s NULLS LAST, a.triggered_at DESC, a.id DESC", expr, dir)
}
