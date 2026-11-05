package oncall

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

var update = flag.Bool("update", false, "rewrite golden files")

var (
	alice = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	bob   = uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	carol = uuid.MustParse("00000000-0000-0000-0000-00000000000c")
	dave  = uuid.MustParse("00000000-0000-0000-0000-00000000000d")
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func expectOnCall(t *testing.T, s Schedule, at string, want uuid.UUID) {
	t.Helper()
	a, ok := s.OnCallAt(utc(at))
	if want == uuid.Nil {
		if ok {
			t.Errorf("at %s: %s on call, want nobody", at, a.UserID)
		}
		return
	}
	if !ok || a.UserID != want {
		t.Errorf("at %s: on call %s (ok=%v), want %s", at, a.UserID, ok, want)
	}
}

type span struct {
	user       uuid.UUID
	start, end string
}

func expectPreview(t *testing.T, got []Shift, want []span) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("preview has %d shifts, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.UserID != w.user || !g.Start.Equal(utc(w.start)) || !g.End.Equal(utc(w.end)) {
			t.Errorf("shift %d = %s [%s, %s), want %s [%s, %s)", i, g.UserID,
				g.Start.UTC().Format(time.RFC3339), g.End.UTC().Format(time.RFC3339), w.user, w.start, w.end)
		}
	}
}

// A daily 09:00 handoff in Paris stays at 09:00 local through the spring
// change (2026-03-29), so that shift lasts 23 hours.
func TestDailyRotationAcrossSpringForward(t *testing.T) {
	paris := zone(t, "Europe/Paris")
	s := Schedule{Location: paris, Rotations: []Rotation{{
		ID: uuid.New(), Kind: Daily, HandoffMinute: 9 * 60,
		StartAt: time.Date(2026, 3, 27, 9, 0, 0, 0, paris),
		Members: []uuid.UUID{alice, bob, carol},
	}}}
	expectOnCall(t, s, "2026-03-27T07:59:00Z", uuid.Nil) // before the start
	expectOnCall(t, s, "2026-03-27T08:00:00Z", alice)
	expectOnCall(t, s, "2026-03-28T07:59:00Z", alice)
	expectOnCall(t, s, "2026-03-28T08:00:00Z", bob)
	expectOnCall(t, s, "2026-03-29T06:59:00Z", bob) // 08:59 CEST
	expectOnCall(t, s, "2026-03-29T07:00:00Z", carol)
	expectOnCall(t, s, "2026-03-30T07:00:00Z", alice) // wraps
	expectOnCall(t, s, "2026-04-30T07:00:00Z", bob)   // 34 handoffs later

	expectPreview(t, s.Preview(utc("2026-03-27T00:00:00Z"), utc("2026-03-30T07:00:00Z")), []span{
		{alice, "2026-03-27T08:00:00Z", "2026-03-28T08:00:00Z"},
		{bob, "2026-03-28T08:00:00Z", "2026-03-29T07:00:00Z"},
		{carol, "2026-03-29T07:00:00Z", "2026-03-30T07:00:00Z"},
	})
}

// A weekly Monday 10:00 New York handoff across the autumn change
// (2026-11-01): the first shift is partial (from StartAt), the second one is
// 7 days and one hour long.
func TestWeeklyRotationAcrossFallBack(t *testing.T) {
	ny := zone(t, "America/New_York")
	s := Schedule{Location: ny, Rotations: []Rotation{{
		ID: uuid.New(), Kind: Weekly, HandoffMinute: 10 * 60, HandoffWeekday: time.Monday,
		StartAt: time.Date(2026, 10, 28, 12, 0, 0, 0, ny), // Wednesday
		Members: []uuid.UUID{alice, bob},
	}}}
	expectPreview(t, s.Preview(utc("2026-10-28T00:00:00Z"), utc("2026-11-16T15:00:00Z")), []span{
		{alice, "2026-10-28T16:00:00Z", "2026-11-02T15:00:00Z"},
		{bob, "2026-11-02T15:00:00Z", "2026-11-09T15:00:00Z"},
		{alice, "2026-11-09T15:00:00Z", "2026-11-16T15:00:00Z"},
	})
	expectOnCall(t, s, "2026-11-02T14:59:59Z", alice)
	expectOnCall(t, s, "2026-11-02T15:00:00Z", bob)
}

// A handoff inside the spring-forward gap still advances exactly once a day.
func TestHandoffInsideDSTGap(t *testing.T) {
	paris := zone(t, "Europe/Paris")
	r := Rotation{
		ID: uuid.New(), Kind: Daily, HandoffMinute: 2*60 + 30,
		StartAt: time.Date(2026, 3, 25, 2, 30, 0, 0, paris),
		Members: []uuid.UUID{alice, bob},
	}
	hs := r.handoffs(utc("2026-03-25T00:00:00Z"), utc("2026-04-02T00:00:00Z"), paris)
	if len(hs) != 7 {
		t.Fatalf("handoffs = %v, want one a day after the start", hs)
	}
	for i := 1; i < len(hs); i++ {
		if !hs[i].After(hs[i-1]) {
			t.Fatalf("handoffs not increasing: %v", hs)
		}
	}
	s := Schedule{Location: paris, Rotations: []Rotation{r}}
	for i, h := range hs {
		want := alice
		if i%2 == 0 {
			want = bob
		}
		if a, _ := s.OnCallAt(h); a.UserID != want {
			t.Errorf("handoff %d (%s): %s on call, want %s", i, h, a.UserID, want)
		}
	}
}

// Overrides beat rotations; of overlapping overrides the newest wins.
func TestOverridePrecedence(t *testing.T) {
	s := Schedule{Location: time.UTC, Rotations: []Rotation{{
		ID: uuid.New(), Kind: Daily, HandoffMinute: 0,
		StartAt: utc("2026-06-01T00:00:00Z"),
		Members: []uuid.UUID{alice},
	}}, Overrides: []Override{
		{ID: uuid.New(), UserID: bob, Start: utc("2026-06-02T08:00:00Z"), End: utc("2026-06-02T20:00:00Z"), CreatedAt: utc("2026-05-01T00:00:00Z")},
		{ID: uuid.New(), UserID: carol, Start: utc("2026-06-02T12:00:00Z"), End: utc("2026-06-02T14:00:00Z"), CreatedAt: utc("2026-05-02T00:00:00Z")},
	}}
	expectOnCall(t, s, "2026-06-02T07:59:59Z", alice)
	expectOnCall(t, s, "2026-06-02T08:00:00Z", bob)
	expectOnCall(t, s, "2026-06-02T12:30:00Z", carol)
	expectOnCall(t, s, "2026-06-02T20:00:00Z", alice)
	if a, _ := s.OnCallAt(utc("2026-06-02T13:00:00Z")); a.Source != SourceOverride || a.OverrideID != s.Overrides[1].ID {
		t.Errorf("assignment = %+v, want the newer override", a)
	}

	expectPreview(t, s.Preview(utc("2026-06-02T00:00:00Z"), utc("2026-06-03T00:00:00Z")), []span{
		{alice, "2026-06-02T00:00:00Z", "2026-06-02T08:00:00Z"},
		{bob, "2026-06-02T08:00:00Z", "2026-06-02T12:00:00Z"},
		{carol, "2026-06-02T12:00:00Z", "2026-06-02T14:00:00Z"},
		{bob, "2026-06-02T14:00:00Z", "2026-06-02T20:00:00Z"},
		{alice, "2026-06-02T20:00:00Z", "2026-06-03T00:00:00Z"},
	})

	// An override also covers time no rotation does.
	empty := Schedule{Overrides: s.Overrides[:1]}
	expectOnCall(t, empty, "2026-06-02T09:00:00Z", bob)
	expectOnCall(t, empty, "2026-06-02T21:00:00Z", uuid.Nil)
}

// A later layer takes over while it is active; the one below resumes after
// its end, and a gap with no layer leaves nobody on call.
func TestLayers(t *testing.T) {
	takeoverEnd := utc("2026-06-10T00:00:00Z")
	baseEnd := utc("2026-06-20T00:00:00Z")
	s := Schedule{Location: time.UTC, Rotations: []Rotation{
		{ID: uuid.New(), Kind: Weekly, HandoffWeekday: time.Monday, StartAt: utc("2026-06-01T00:00:00Z"), EndAt: &baseEnd, Members: []uuid.UUID{alice, bob}},
		{ID: uuid.New(), Kind: Daily, HandoffMinute: 0, StartAt: utc("2026-06-05T00:00:00Z"), EndAt: &takeoverEnd, Members: []uuid.UUID{carol}},
		{ID: uuid.New(), Kind: Daily, StartAt: utc("2026-06-01T00:00:00Z")}, // no members: ignored
	}}
	expectOnCall(t, s, "2026-06-04T23:59:59Z", alice)
	expectOnCall(t, s, "2026-06-05T00:00:00Z", carol)
	expectOnCall(t, s, "2026-06-09T23:59:59Z", carol)
	expectOnCall(t, s, "2026-06-10T00:00:00Z", bob) // week of June 8
	expectOnCall(t, s, "2026-06-20T00:00:00Z", uuid.Nil)

	expectPreview(t, s.Preview(utc("2026-06-01T00:00:00Z"), utc("2026-06-25T00:00:00Z")), []span{
		{alice, "2026-06-01T00:00:00Z", "2026-06-05T00:00:00Z"},
		{carol, "2026-06-05T00:00:00Z", "2026-06-10T00:00:00Z"},
		{bob, "2026-06-10T00:00:00Z", "2026-06-15T00:00:00Z"},
		{alice, "2026-06-15T00:00:00Z", "2026-06-20T00:00:00Z"},
	})
}

func TestPreviewWindowIsCapped(t *testing.T) {
	s := Schedule{Rotations: []Rotation{{ID: uuid.New(), Kind: Daily, StartAt: utc("2026-01-01T00:00:00Z"), Members: []uuid.UUID{alice, bob}}}}
	got := s.Preview(utc("2026-01-01T00:00:00Z"), utc("2027-01-01T00:00:00Z"))
	if end := got[len(got)-1].End; end.Sub(utc("2026-01-01T00:00:00Z")) != MaxPreview {
		t.Fatalf("preview ends %s, want MaxPreview after the start", end)
	}
	if s.Preview(utc("2026-01-02T00:00:00Z"), utc("2026-01-01T00:00:00Z")) != nil {
		t.Fatal("an empty window has no shifts")
	}
}

func TestRotationValidate(t *testing.T) {
	end := utc("2026-01-01T00:00:00Z")
	for name, r := range map[string]Rotation{
		"kind":    {Kind: "monthly", StartAt: end},
		"minute":  {Kind: Daily, HandoffMinute: 1440, StartAt: end},
		"weekday": {Kind: Weekly, HandoffWeekday: 7, StartAt: end},
		"start":   {Kind: Daily},
		"end":     {Kind: Daily, StartAt: end, EndAt: &end},
	} {
		if r.Validate() == nil {
			t.Errorf("%s: want a validation error", name)
		}
	}
	if err := (Rotation{Kind: Weekly, HandoffWeekday: time.Saturday, HandoffMinute: 1439, StartAt: end}).Validate(); err != nil {
		t.Errorf("valid rotation: %v", err)
	}
}

func TestCalendarGolden(t *testing.T) {
	cal := Calendar{
		Name:  "Platform; primary, EU",
		Stamp: utc("2026-11-05T10:00:00Z"),
		Events: []CalendarEvent{
			{UID: "s1-1@probara", Start: utc("2026-11-02T15:00:00Z"), End: utc("2026-11-09T15:00:00Z"),
				Summary: "On call: alice", Description: "Platform; primary, EU\nRotation shift"},
			{UID: "s1-2@probara", Start: utc("2026-11-09T15:00:00Z"), End: utc("2026-11-10T15:00:00Z"),
				Summary:     "On call: bob\r\nATTENDEE:mailto:evil@example.com",
				Description: "Override: a long description that goes on well past the seventy-five octet line limit, with ünïcödé"},
		},
	}
	got := cal.Render()
	path := filepath.Join("testdata", "feed.ics")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("feed differs from %s (run with -update):\n%s", path, got)
	}
	for _, l := range splitCRLF(got) {
		if len(l) > 75 {
			t.Errorf("line longer than 75 octets: %q", l)
		}
	}
}

func splitCRLF(s string) []string {
	var out []string
	for len(s) > 0 {
		i := indexCRLF(s)
		if i < 0 {
			return append(out, s)
		}
		out = append(out, s[:i])
		s = s[i+2:]
	}
	return out
}

func indexCRLF(s string) int {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '\r' && s[i+1] == '\n' {
			return i
		}
	}
	return -1
}
