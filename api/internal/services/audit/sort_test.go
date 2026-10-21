package audit

import "testing"

func TestParseSort(t *testing.T) {
	cases := []struct {
		column, dir string
		want        string
		desc        bool
	}{
		{"", "", SortOccurredAt, true},
		{"occurred_at", "asc", SortOccurredAt, false},
		{"action", "", SortAction, false},
		{"resource", "desc", SortResource, true},
		{"outcome", "", SortOutcome, false},
	}
	for _, c := range cases {
		got, desc, err := ParseSort(c.column, c.dir)
		if err != nil {
			t.Fatalf("ParseSort(%q, %q): %v", c.column, c.dir, err)
		}
		if got != c.want || desc != c.desc {
			t.Errorf("ParseSort(%q, %q) = %q,%v want %q,%v", c.column, c.dir, got, desc, c.want, c.desc)
		}
	}
	for _, bad := range [][2]string{{"ip", ""}, {"id; --", ""}, {"action", "sideways"}} {
		if _, _, err := ParseSort(bad[0], bad[1]); err == nil {
			t.Errorf("ParseSort(%q, %q) accepted a value outside the whitelist", bad[0], bad[1])
		}
	}
}

func TestOrderBy(t *testing.T) {
	cases := []struct {
		column string
		desc   bool
		want   string
	}{
		{"", false, "occurred_at DESC, id DESC"},
		{SortOccurredAt, false, "occurred_at ASC, id ASC"},
		{SortAction, false, "action ASC, occurred_at DESC, id DESC"},
		{SortResource, true, "resource_type DESC, resource_id DESC, occurred_at DESC, id DESC"},
		{SortOutcome, false, "outcome ASC, occurred_at DESC, id DESC"},
		{"bogus", false, "occurred_at DESC, id DESC"},
	}
	for _, c := range cases {
		if got := orderBy(c.column, c.desc); got != c.want {
			t.Errorf("orderBy(%q, %v) = %q, want %q", c.column, c.desc, got, c.want)
		}
	}
}
