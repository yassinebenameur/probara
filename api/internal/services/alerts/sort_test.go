package alerts

import (
	"strings"
	"testing"
)

func TestParseListSort(t *testing.T) {
	cases := []struct {
		column, dir string
		want        ListSort
	}{
		{"", "", ListSort{Column: SortTriggeredAt, Desc: true}},
		{"triggered_at", "", ListSort{Column: SortTriggeredAt, Desc: true}},
		{"triggered_at", "asc", ListSort{Column: SortTriggeredAt}},
		{"duration", "", ListSort{Column: SortDuration, Desc: true}},
		{"status", "", ListSort{Column: SortStatus}},
		{"monitor", "desc", ListSort{Column: SortMonitor, Desc: true}},
	}
	for _, c := range cases {
		got, err := ParseListSort(c.column, c.dir)
		if err != nil {
			t.Fatalf("ParseListSort(%q, %q): %v", c.column, c.dir, err)
		}
		if got != c.want {
			t.Errorf("ParseListSort(%q, %q) = %+v, want %+v", c.column, c.dir, got, c.want)
		}
	}

	for _, bad := range [][2]string{{"a.id; DROP TABLE alerts", ""}, {"name", ""}, {"status", "up"}, {"status", "ASC"}} {
		if _, err := ParseListSort(bad[0], bad[1]); err == nil {
			t.Errorf("ParseListSort(%q, %q) accepted a value outside the whitelist", bad[0], bad[1])
		}
	}
}

func TestListSortOrderByEndsInIDTiebreaker(t *testing.T) {
	if got := (ListSort{}).orderBy(); got != "a.triggered_at DESC, a.id DESC" {
		t.Errorf("zero value orderBy = %q, want newest first", got)
	}
	if got := (ListSort{Column: SortTriggeredAt}).orderBy(); got != "a.triggered_at ASC, a.id ASC" {
		t.Errorf("oldest first orderBy = %q", got)
	}
	for column := range alertSortColumns {
		for _, desc := range []bool{false, true} {
			got := ListSort{Column: column, Desc: desc}.orderBy()
			if !strings.HasSuffix(got, "a.id DESC") && !strings.HasSuffix(got, "a.id ASC") {
				t.Errorf("%s desc=%v: %q has no id tiebreaker", column, desc, got)
			}
		}
	}
	// An unknown column (never produced by ParseListSort) falls back to the
	// default instead of reaching SQL.
	if got := (ListSort{Column: "x"}).orderBy(); got != "a.triggered_at DESC, a.id DESC" {
		t.Errorf("unknown column orderBy = %q", got)
	}
}
