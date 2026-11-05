//go:build !unit

package oncall

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/oncall"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type fixture struct {
	ctx    context.Context
	s      *Service
	tenant uuid.UUID
	other  uuid.UUID
	users  map[string]uuid.UUID
}

func setup(t *testing.T, now time.Time) *fixture {
	t.Helper()
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	f := &fixture{
		ctx:    ctx,
		s:      NewService(dbClient, "https://probara.example.com/"),
		tenant: testutil.InsertTenant(ctx, t, dbClient, "oncall"),
		other:  testutil.InsertTenant(ctx, t, dbClient, "other"),
		users:  map[string]uuid.UUID{},
	}
	f.s.now = func() time.Time { return now }
	for _, u := range []struct {
		name, platform string
		tenant         uuid.UUID
	}{
		{"alice", "member", f.tenant},
		{"bob", "member", f.tenant},
		{"outsider", "member", f.other},
		{"root", "superadmin", uuid.Nil},
		{"ops", "superadmin", uuid.Nil},
	} {
		id := uuid.New()
		if _, err := dbClient.ExecContext(ctx, `INSERT INTO admin_users (id, username, password_hash, platform_role) VALUES ($1, $2, 'x', $3)`,
			id, u.name, u.platform); err != nil {
			t.Fatal(err)
		}
		if u.tenant != uuid.Nil {
			if _, err := dbClient.ExecContext(ctx, `INSERT INTO tenant_memberships (admin_user_id, tenant_id, role) VALUES ($1, $2, 'viewer')`,
				id, u.tenant); err != nil {
				t.Fatal(err)
			}
		}
		f.users[u.name] = id
	}
	return f
}

func intp(i int) *int { return &i }

func (f *fixture) peopleNames(t *testing.T, tenantID uuid.UUID, allSuperadmins bool) string {
	t.Helper()
	people, err := f.s.People(f.ctx, tenantID, allSuperadmins)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, p := range people {
		names = append(names, p.Username+":"+p.Role)
	}
	return strings.Join(names, ",")
}

// The picker lists the workspace's members, never the installation's
// superadmins wholesale: one shows up only once they are on one of the
// workspace's schedules, or when the caller is a superadmin. Writes still
// take any eligible user by ID.
func TestPeopleListing(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	f := setup(t, now)

	if got := f.peopleNames(t, f.tenant, false); got != "alice:viewer,bob:viewer" {
		t.Fatalf("members only: people = %v", got)
	}
	if got := f.peopleNames(t, f.tenant, true); got != "alice:viewer,bob:viewer,ops:superadmin,root:superadmin" {
		t.Fatalf("superadmin caller: people = %v", got)
	}

	// A superadmin is still eligible by ID, though not listed.
	sched, err := f.s.Create(f.ctx, f.tenant, ScheduleInput{
		Name: "Primary", Timezone: "UTC",
		Rotations: []RotationInput{{Kind: oncall.Daily, HandoffTime: "09:00",
			StartAt: now.Add(-time.Hour), MemberIDs: []uuid.UUID{f.users["alice"]}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CreateOverride(f.ctx, f.tenant, sched.ID, f.users["alice"], CreateOverrideRequest{
		UserID: f.users["root"], StartsAt: now, EndsAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("superadmin override: %v", err)
	}
	if got := f.peopleNames(t, f.tenant, false); got != "alice:viewer,bob:viewer,root:superadmin" {
		t.Fatalf("after override: people = %v", got)
	}
	// Another workspace does not see root through this one's schedules.
	if got := f.peopleNames(t, f.other, false); got != "outsider:viewer" {
		t.Fatalf("other workspace: people = %v", got)
	}
}

func TestScheduleLifecycle(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) // Wednesday
	f := setup(t, now)
	alice, bob := f.users["alice"], f.users["bob"]

	in := ScheduleInput{
		Name: "Platform primary", Timezone: "Europe/Paris",
		Rotations: []RotationInput{{
			Name: "Weekly", Kind: oncall.Weekly, HandoffTime: "09:00", HandoffWeekday: intp(1),
			StartAt:   time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC), // Monday 09:00 CET
			MemberIDs: []uuid.UUID{alice, bob},
		}},
	}
	// Validation: members must belong to the workspace; zones must be real.
	bad := in
	bad.Rotations = []RotationInput{in.Rotations[0]}
	bad.Rotations[0].MemberIDs = []uuid.UUID{f.users["outsider"]}
	if _, err := f.s.Create(f.ctx, f.tenant, bad); !errors.As(err, new(Invalid)) {
		t.Fatalf("outsider member: err = %v, want Invalid", err)
	}
	bad = in
	bad.Timezone = "Mars/Olympus"
	if _, err := f.s.Create(f.ctx, f.tenant, bad); !errors.As(err, new(Invalid)) {
		t.Fatalf("bad zone: err = %v, want Invalid", err)
	}
	bad = in
	bad.Rotations = []RotationInput{in.Rotations[0]}
	bad.Rotations[0].HandoffWeekday = nil
	if _, err := f.s.Create(f.ctx, f.tenant, bad); !errors.As(err, new(Invalid)) {
		t.Fatalf("weekly without weekday: err = %v, want Invalid", err)
	}

	sched, err := f.s.Create(f.ctx, f.tenant, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Create(f.ctx, f.tenant, in); !errors.As(err, new(Conflict)) {
		t.Fatalf("duplicate name: err = %v, want Conflict", err)
	}
	if sched.OnCall == nil || sched.OnCall.User.ID != alice || sched.Next == nil || sched.Next.User.ID != bob {
		t.Fatalf("on call = %+v next = %+v", sched.OnCall, sched.Next)
	}
	if !sched.OnCall.End.Equal(time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("shift ends %s, want next Monday 09:00 Paris", sched.OnCall.End)
	}
	if r := sched.Rotations[0]; r.HandoffTime != "09:00" || r.HandoffWeekday == nil || *r.HandoffWeekday != 1 ||
		len(r.Members) != 2 || r.Members[0].Username != "alice" || !r.Members[0].Eligible {
		t.Fatalf("rotation = %+v", r)
	}
	if _, err := f.s.Get(f.ctx, f.other, sched.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other tenant: err = %v, want ErrNotFound", err)
	}

	// An override puts bob on call over Wednesday afternoon.
	ov, err := f.s.CreateOverride(f.ctx, f.tenant, sched.ID, alice, CreateOverrideRequest{
		UserID: bob, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(4 * time.Hour), Note: "swap",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ov.User.ID != bob || ov.CreatedBy == nil || ov.CreatedBy.ID != alice {
		t.Fatalf("override = %+v", ov)
	}
	if _, err := f.s.CreateOverride(f.ctx, f.tenant, sched.ID, alice, CreateOverrideRequest{
		UserID: f.users["outsider"], StartsAt: now, EndsAt: now.Add(time.Hour),
	}); !errors.As(err, new(Invalid)) {
		t.Fatalf("outsider override: err = %v, want Invalid", err)
	}
	cur, err := f.s.OnCall(f.ctx, f.tenant, sched.ID, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if cur.OnCall == nil || cur.OnCall.User.ID != bob || cur.OnCall.Source != oncall.SourceOverride ||
		cur.Next == nil || cur.Next.User.ID != alice || !cur.Next.Start.Equal(now.Add(4*time.Hour)) {
		t.Fatalf("on call with override = %+v / %+v", cur.OnCall, cur.Next)
	}

	pv, err := f.s.Preview(f.ctx, f.tenant, sched.ID, now, now.Add(7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Shifts) != 3 || pv.Shifts[0].User.ID != bob || pv.Shifts[1].User.ID != alice || pv.Shifts[2].User.ID != bob {
		t.Fatalf("preview = %+v", pv.Shifts)
	}
	if _, err := f.s.Preview(f.ctx, f.tenant, sched.ID, now, now.Add(100*24*time.Hour)); !errors.As(err, new(Invalid)) {
		t.Fatalf("long preview: err = %v, want Invalid", err)
	}

	list, err := f.s.ListOverrides(f.ctx, f.tenant, sched.ID, time.Time{}, time.Time{})
	if err != nil || len(list) != 1 {
		t.Fatalf("overrides = %+v, %v", list, err)
	}
	if err := f.s.DeleteOverride(f.ctx, f.other, sched.ID, ov.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other tenant delete: err = %v", err)
	}
	if err := f.s.DeleteOverride(f.ctx, f.tenant, sched.ID, ov.ID); err != nil {
		t.Fatal(err)
	}

	// Replace swaps the rotation for a daily one with a superadmin in it.
	in.Rotations = []RotationInput{{Kind: oncall.Daily, HandoffTime: "08:30",
		StartAt: now.Add(-48 * time.Hour), MemberIDs: []uuid.UUID{f.users["root"], alice}}}
	in.Description = "Daily now"
	updated, err := f.s.Replace(f.ctx, f.tenant, sched.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Rotations) != 1 || updated.Rotations[0].Kind != oncall.Daily || updated.Description != "Daily now" {
		t.Fatalf("replaced = %+v", updated)
	}

	// root is on a rotation now, so the picker lists them; ops is not.
	if got := f.peopleNames(t, f.tenant, false); got != "alice:viewer,bob:viewer,root:superadmin" {
		t.Fatalf("people = %v", got)
	}

	all, err := f.s.List(f.ctx, f.tenant)
	if err != nil || len(all) != 1 || all[0].OnCall == nil {
		t.Fatalf("list = %+v, %v", all, err)
	}
	if err := f.s.Delete(f.ctx, f.tenant, sched.ID); err != nil {
		t.Fatal(err)
	}
}

func TestICalFeed(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	f := setup(t, now)
	sched, err := f.s.Create(f.ctx, f.tenant, ScheduleInput{
		Name: "Edge", Timezone: "UTC",
		Rotations: []RotationInput{{Name: "Daily", Kind: oncall.Daily, HandoffTime: "00:00",
			StartAt: now.Add(-24 * time.Hour), MemberIDs: []uuid.UUID{f.users["alice"], f.users["bob"]}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Feed(f.ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token: err = %v", err)
	}
	tok, err := f.s.RotateICalToken(f.ctx, f.tenant, sched.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok.URL, "https://probara.example.com/api/v1/oncall-feeds/") || !strings.HasSuffix(tok.Path, ".ics") ||
		strings.Contains(tok.Token, ".") {
		t.Fatalf("token = %+v", tok)
	}
	var stored string
	if err := f.s.db.QueryRowContext(f.ctx, `SELECT ical_token_hash FROM oncall_schedules WHERE id = $1`, sched.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == tok.Token || stored != hashToken(tok.Token) {
		t.Fatal("only the token's hash is stored")
	}
	body, err := f.s.Feed(f.ctx, tok.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "X-WR-CALNAME:On call: Edge") || !strings.Contains(body, "SUMMARY:On call: alice") ||
		!strings.Contains(body, "SUMMARY:On call: bob") || !strings.Contains(body, "\r\n") {
		t.Fatalf("feed = %s", body)
	}

	// Rotating revokes the old link; a deleted workspace's feed is gone.
	tok2, err := f.s.RotateICalToken(f.ctx, f.tenant, sched.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Feed(f.ctx, tok.Token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rotated token: err = %v", err)
	}
	if _, err := f.s.db.ExecContext(f.ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, f.tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Feed(f.ctx, tok2.Token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted workspace: err = %v", err)
	}
	if _, err := f.s.db.ExecContext(f.ctx, `UPDATE tenants SET deleted_at = NULL WHERE id = $1`, f.tenant); err != nil {
		t.Fatal(err)
	}
	if err := f.s.DisableICal(f.ctx, f.tenant, sched.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Feed(f.ctx, tok2.Token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled feed: err = %v", err)
	}
}
