package audit

import "fmt"

// Sortable audit log columns (the `sort` query parameter).
const (
	SortOccurredAt = "occurred_at"
	SortAction     = "action"
	SortResource   = "resource"
	SortOutcome    = "outcome"
)

// auditSortColumns whitelists the sortable columns: nothing from the request
// reaches ORDER BY except through this table. Ties read newest first, then by
// id, so OFFSET paging is deterministic. occurred_at and action orders follow
// the (tenant_id, occurred_at) and (tenant_id, action, occurred_at) indexes.
var auditSortColumns = map[string]string{
	SortOccurredAt: "occurred_at",
	SortAction:     "action",
	SortResource:   "resource_type %[1]s, resource_id",
	SortOutcome:    "outcome",
}

// ParseSort validates the `sort` / `dir` query parameters into a Filters
// sort. An empty column is the default (occurred_at); an empty dir is the
// column's natural direction (newest first for time, A-Z otherwise).
func ParseSort(column, dir string) (string, bool, error) {
	if column == "" {
		column = SortOccurredAt
	}
	if _, ok := auditSortColumns[column]; !ok {
		return "", false, fmt.Errorf("invalid sort %q: expected one of occurred_at, action, resource, outcome", column)
	}
	switch dir {
	case "":
		return column, column == SortOccurredAt, nil
	case "asc":
		return column, false, nil
	case "desc":
		return column, true, nil
	default:
		return "", false, fmt.Errorf("invalid dir %q: expected asc or desc", dir)
	}
}

// orderBy renders the ORDER BY list for a sort ("" = newest first).
func orderBy(column string, desc bool) string {
	expr, ok := auditSortColumns[column]
	if !ok || column == "" {
		column, expr, desc = SortOccurredAt, auditSortColumns[SortOccurredAt], true
	}
	dir := "ASC"
	if desc {
		dir = "DESC"
	}
	if column == SortOccurredAt {
		return fmt.Sprintf("occurred_at %s, id %s", dir, dir)
	}
	return fmt.Sprintf(expr+" %[1]s, occurred_at DESC, id DESC", dir)
}
