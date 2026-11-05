// Package oncall stores on-call schedules (rotations, members, overrides,
// the iCal feed token) and answers who is on call through the pure resolver
// in shared/oncall. Schedules are informational for now: nothing pages them
// until escalation policies land.
package oncall

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lib/pq"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/oncall"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

// FeedPathPrefix is the public iCal feed route below the API origin.
const FeedPathPrefix = "/api/v1/oncall-feeds/"

// Service implements the schedule store.
type Service struct {
	db            *shareddb.Client
	publicBaseURL string
	now           func() time.Time
}

// NewService builds the service. publicBaseURL (PUBLIC_BASE_URL) makes feed
// URLs absolute.
func NewService(db *shareddb.Client, publicBaseURL string) *Service {
	return &Service{db: db, publicBaseURL: strings.TrimRight(publicBaseURL, "/"), now: time.Now}
}

// ---- validation ----

func parseHandoff(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || len(h) == 0 || len(h) > 2 || len(m) != 2 {
		return 0, invalid("handoff_time must be HH:MM")
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, invalid("handoff_time must be HH:MM between 00:00 and 23:59")
	}
	return hh*60 + mm, nil
}

func formatHandoff(minute int) string { return fmt.Sprintf("%02d:%02d", minute/60, minute%60) }

func loadZone(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, invalid("timezone must be an IANA zone name such as Europe/Paris or UTC")
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, invalid("timezone %q is not a known IANA zone", name)
	}
	return loc, nil
}

func cleanText(field, s string, max int, multiline bool) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		return "", invalid("%s must be at most %d characters", field, max)
	}
	if strings.ContainsFunc(s, func(r rune) bool {
		return (r < 0x20 && !(multiline && (r == '\n' || r == '\t'))) || r == 0x7f
	}) {
		return "", invalid("%s must not contain control characters", field)
	}
	return s, nil
}

type rotationRow struct {
	name    string
	rot     oncall.Rotation
	members []uuid.UUID
}

func (s *Service) validate(ctx context.Context, tenantID uuid.UUID, in ScheduleInput) (name, desc, tz string, rows []rotationRow, err error) {
	if name, err = cleanText("name", in.Name, MaxNameLength, false); err != nil {
		return
	}
	if name == "" {
		err = invalid("name is required")
		return
	}
	if desc, err = cleanText("description", in.Description, MaxDescription, true); err != nil {
		return
	}
	tz = strings.TrimSpace(in.Timezone)
	if tz == "" {
		tz = "UTC"
	}
	if _, err = loadZone(tz); err != nil {
		return
	}
	if len(in.Rotations) > MaxRotations {
		err = invalid("a schedule has at most %d rotations", MaxRotations)
		return
	}
	var userIDs []uuid.UUID
	for i, ri := range in.Rotations {
		var rn string
		if rn, err = cleanText(fmt.Sprintf("rotations[%d].name", i), ri.Name, MaxNameLength, false); err != nil {
			return
		}
		r := oncall.Rotation{Kind: ri.Kind, StartAt: ri.StartAt.UTC(), Members: ri.MemberIDs}
		if ri.EndAt != nil {
			end := ri.EndAt.UTC()
			r.EndAt = &end
		}
		if r.HandoffMinute, err = parseHandoff(ri.HandoffTime); err != nil {
			err = invalid("rotations[%d]: %s", i, err.Error())
			return
		}
		switch {
		case ri.Kind == oncall.Weekly && ri.HandoffWeekday == nil:
			err = invalid("rotations[%d]: handoff_weekday is required for a weekly rotation", i)
			return
		case ri.Kind == oncall.Weekly:
			r.HandoffWeekday = time.Weekday(*ri.HandoffWeekday)
			if *ri.HandoffWeekday < 0 || *ri.HandoffWeekday > 6 {
				err = invalid("rotations[%d]: handoff_weekday must be 0 (Sunday) to 6 (Saturday)", i)
				return
			}
		case ri.HandoffWeekday != nil:
			err = invalid("rotations[%d]: handoff_weekday applies to weekly rotations only", i)
			return
		}
		if verr := r.Validate(); verr != nil {
			err = invalid("rotations[%d]: %s", i, verr.Error())
			return
		}
		if len(ri.MemberIDs) == 0 {
			err = invalid("rotations[%d]: add at least one member", i)
			return
		}
		if len(ri.MemberIDs) > MaxMembers {
			err = invalid("rotations[%d]: at most %d members", i, MaxMembers)
			return
		}
		userIDs = append(userIDs, ri.MemberIDs...)
		rows = append(rows, rotationRow{name: rn, rot: r, members: ri.MemberIDs})
	}
	if err = s.requireEligible(ctx, tenantID, userIDs); err != nil {
		return
	}
	return
}

// requireEligible refuses users who cannot be on call in the workspace.
func (s *Service) requireEligible(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	refs, err := s.userRefs(ctx, tenantID, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		ref, ok := refs[id]
		if !ok || !ref.Eligible {
			return invalid("user %s is not an active member of this workspace", id)
		}
	}
	return nil
}

// eligibleSQL holds for a user who can be on call in tenant $1: enabled, and
// a superadmin or a member of the workspace.
const eligibleSQL = `(u.disabled_at IS NULL AND (u.platform_role = 'superadmin' OR m.admin_user_id IS NOT NULL))`

const verifiedContactsSQL = `(SELECT COUNT(*) FROM user_contacts c WHERE c.user_id = u.id AND c.verified_at IS NOT NULL)`

func (s *Service) userRefs(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]UserRef, error) {
	out := map[uuid.UUID]UserRef{}
	if len(ids) == 0 {
		return out, nil
	}
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = id.String()
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.username, `+eligibleSQL+`, `+verifiedContactsSQL+`
		FROM admin_users u
		LEFT JOIN tenant_memberships m ON m.admin_user_id = u.id AND m.tenant_id = $1
		WHERE u.id = ANY($2::uuid[])
	`, tenantID, pq.Array(strs))
	if err != nil {
		return nil, fmt.Errorf("load users: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r UserRef
		if err := rows.Scan(&r.ID, &r.Username, &r.Eligible, &r.VerifiedContacts); err != nil {
			return nil, err
		}
		out[r.ID] = r
	}
	return out, rows.Err()
}

func refFor(refs map[uuid.UUID]UserRef, id uuid.UUID) UserRef {
	if r, ok := refs[id]; ok {
		return r
	}
	return UserRef{ID: id, Username: "(deleted user)"}
}

// scheduledInTenantSQL holds for a user already on one of tenant $1's
// rotations or overrides; schedule reads show those usernames to every role.
const scheduledInTenantSQL = `(EXISTS (
		SELECT 1 FROM oncall_rotation_members rm
		JOIN oncall_rotations r ON r.id = rm.rotation_id
		JOIN oncall_schedules sc ON sc.id = r.schedule_id
		WHERE sc.tenant_id = $1 AND rm.user_id = u.id
	) OR EXISTS (
		SELECT 1 FROM oncall_overrides o
		JOIN oncall_schedules sc ON sc.id = o.schedule_id
		WHERE sc.tenant_id = $1 AND o.user_id = u.id
	))`

// People lists the picker for putting someone on call in the workspace: its
// enabled members, plus the superadmins already on one of its schedules.
// allSuperadmins (the caller is a superadmin, who can list users anyway)
// adds every enabled superadmin. The narrowing is deliberate: superadmins
// belong to no workspace, and a known username is enough to lock an account
// out, so a workspace must not learn the installation's operators from here.
// Writes still accept any eligible user by ID (requireEligible).
func (s *Service) People(ctx context.Context, tenantID uuid.UUID, allSuperadmins bool) ([]Person, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.username,
			CASE WHEN u.platform_role = 'superadmin' THEN 'superadmin' ELSE m.role END,
			`+verifiedContactsSQL+`
		FROM admin_users u
		LEFT JOIN tenant_memberships m ON m.admin_user_id = u.id AND m.tenant_id = $1
		WHERE `+eligibleSQL+`
			AND (m.admin_user_id IS NOT NULL OR $2 OR `+scheduledInTenantSQL+`)
		ORDER BY lower(u.username)
	`, tenantID, allSuperadmins)
	if err != nil {
		return nil, fmt.Errorf("list people: %w", err)
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		p := Person{UserRef: UserRef{Eligible: true}}
		if err := rows.Scan(&p.ID, &p.Username, &p.Role, &p.VerifiedContacts); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- loading ----

type loaded struct {
	Schedule
	sched oncall.Schedule
	refs  map[uuid.UUID]UserRef
}

// load reads the tenant's schedules (one when id is set) with their
// rotations and the overrides overlapping [from, to).
func (s *Service) load(ctx context.Context, tenantID uuid.UUID, id *uuid.UUID, from, to time.Time) ([]*loaded, error) {
	query := `SELECT id, name, description, timezone, ical_token_hash IS NOT NULL, created_at, updated_at
		FROM oncall_schedules WHERE tenant_id = $1`
	args := []any{tenantID}
	if id != nil {
		query += ` AND id = $2`
		args = append(args, *id)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY lower(name), id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	var out []*loaded
	byID := map[uuid.UUID]*loaded{}
	var ids []string
	for rows.Next() {
		l := &loaded{}
		if err := rows.Scan(&l.ID, &l.Name, &l.Description, &l.Timezone, &l.ICalEnabled, &l.CreatedAt, &l.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		l.Rotations = []Rotation{}
		l.sched.Location = time.UTC
		if loc, err := time.LoadLocation(l.Timezone); err == nil {
			l.sched.Location = loc
		}
		out = append(out, l)
		byID[l.ID] = l
		ids = append(ids, l.ID.String())
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	userSet := map[uuid.UUID]bool{}
	rotRows, err := s.db.QueryContext(ctx, `
		SELECT r.schedule_id, r.id, r.position, r.name, r.kind, r.handoff_minute, r.handoff_weekday, r.start_at, r.end_at,
			COALESCE(array_agg(m.user_id::text ORDER BY m.position) FILTER (WHERE m.user_id IS NOT NULL), '{}')
		FROM oncall_rotations r
		LEFT JOIN oncall_rotation_members m ON m.rotation_id = r.id
		WHERE r.schedule_id = ANY($1::uuid[])
		GROUP BY r.id
		ORDER BY r.schedule_id, r.position
	`, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("load rotations: %w", err)
	}
	for rotRows.Next() {
		var sid uuid.UUID
		var r Rotation
		var minute int
		var weekday sql.NullInt64
		var members pq.StringArray
		if err := rotRows.Scan(&sid, &r.ID, &r.Position, &r.Name, &r.Kind, &minute, &weekday, &r.StartAt, &r.EndAt, &members); err != nil {
			rotRows.Close()
			return nil, err
		}
		r.HandoffTime = formatHandoff(minute)
		rot := oncall.Rotation{ID: r.ID, Kind: r.Kind, HandoffMinute: minute, StartAt: r.StartAt, EndAt: r.EndAt}
		if weekday.Valid {
			w := int(weekday.Int64)
			r.HandoffWeekday = &w
			rot.HandoffWeekday = time.Weekday(w)
		}
		for _, m := range members {
			uid, err := uuid.Parse(m)
			if err != nil {
				continue
			}
			rot.Members = append(rot.Members, uid)
			r.Members = append(r.Members, UserRef{ID: uid})
			userSet[uid] = true
		}
		if r.Members == nil {
			r.Members = []UserRef{}
		}
		l := byID[sid]
		l.Rotations = append(l.Rotations, r)
		l.sched.Rotations = append(l.sched.Rotations, rot)
	}
	rotRows.Close()
	if err := rotRows.Err(); err != nil {
		return nil, err
	}

	ovRows, err := s.db.QueryContext(ctx, `
		SELECT schedule_id, id, user_id, starts_at, ends_at, created_at
		FROM oncall_overrides
		WHERE schedule_id = ANY($1::uuid[]) AND starts_at < $3 AND ends_at > $2
	`, pq.Array(ids), from, to)
	if err != nil {
		return nil, fmt.Errorf("load overrides: %w", err)
	}
	for ovRows.Next() {
		var sid uuid.UUID
		var o oncall.Override
		if err := ovRows.Scan(&sid, &o.ID, &o.UserID, &o.Start, &o.End, &o.CreatedAt); err != nil {
			ovRows.Close()
			return nil, err
		}
		byID[sid].sched.Overrides = append(byID[sid].sched.Overrides, o)
		userSet[o.UserID] = true
	}
	ovRows.Close()
	if err := ovRows.Err(); err != nil {
		return nil, err
	}

	userIDs := make([]uuid.UUID, 0, len(userSet))
	for id := range userSet {
		userIDs = append(userIDs, id)
	}
	refs, err := s.userRefs(ctx, tenantID, userIDs)
	if err != nil {
		return nil, err
	}
	for _, l := range out {
		l.refs = refs
		for i := range l.Rotations {
			for j := range l.Rotations[i].Members {
				l.Rotations[i].Members[j] = refFor(refs, l.Rotations[i].Members[j].ID)
			}
		}
	}
	return out, nil
}

func (s *Service) loadOne(ctx context.Context, tenantID, id uuid.UUID, from, to time.Time) (*loaded, error) {
	all, err := s.load(ctx, tenantID, &id, from, to)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, ErrNotFound
	}
	return all[0], nil
}

func (l *loaded) shift(sh oncall.Shift) Shift {
	out := Shift{User: refFor(l.refs, sh.UserID), Source: sh.Source, Start: sh.Start, End: sh.End}
	if sh.Source == oncall.SourceRotation {
		id := sh.RotationID
		out.RotationID = &id
	} else {
		id := sh.OverrideID
		out.OverrideID = &id
	}
	return out
}

// current fills the shift around at and the one after it. The loaded
// overrides must cover [at-currentWindow, at+currentWindow).
func (l *loaded) current(at time.Time) (cur, next *Shift) {
	for _, sh := range l.sched.Preview(at.Add(-currentWindow), at.Add(currentWindow)) {
		switch {
		case !at.Before(sh.Start) && at.Before(sh.End):
			v := l.shift(sh)
			cur = &v
		case sh.Start.After(at):
			v := l.shift(sh)
			next = &v
			return cur, next
		}
	}
	return cur, next
}

// ---- schedules ----

// List returns the tenant's schedules with who is on call now.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID) ([]Schedule, error) {
	now := s.now().UTC()
	all, err := s.load(ctx, tenantID, nil, now.Add(-currentWindow), now.Add(currentWindow))
	if err != nil {
		return nil, err
	}
	out := make([]Schedule, 0, len(all))
	for _, l := range all {
		l.OnCall, l.Next = l.current(now)
		out = append(out, l.Schedule)
	}
	return out, nil
}

// Get returns one schedule with who is on call now.
func (s *Service) Get(ctx context.Context, tenantID, id uuid.UUID) (*Schedule, error) {
	now := s.now().UTC()
	l, err := s.loadOne(ctx, tenantID, id, now.Add(-currentWindow), now.Add(currentWindow))
	if err != nil {
		return nil, err
	}
	l.OnCall, l.Next = l.current(now)
	return &l.Schedule, nil
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

func insertRotations(ctx context.Context, tx *sql.Tx, scheduleID uuid.UUID, rows []rotationRow) error {
	for pos, r := range rows {
		var weekday *int
		if r.rot.Kind == oncall.Weekly {
			w := int(r.rot.HandoffWeekday)
			weekday = &w
		}
		var rotID uuid.UUID
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO oncall_rotations (schedule_id, position, name, kind, handoff_minute, handoff_weekday, start_at, end_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id
		`, scheduleID, pos, r.name, string(r.rot.Kind), r.rot.HandoffMinute, weekday, r.rot.StartAt, r.rot.EndAt).Scan(&rotID); err != nil {
			return fmt.Errorf("insert rotation: %w", err)
		}
		for mpos, uid := range r.members {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO oncall_rotation_members (rotation_id, position, user_id) VALUES ($1, $2, $3)
			`, rotID, mpos, uid); err != nil {
				return fmt.Errorf("insert rotation member: %w", err)
			}
		}
	}
	return nil
}

// Create adds a schedule.
func (s *Service) Create(ctx context.Context, tenantID uuid.UUID, in ScheduleInput) (*Schedule, error) {
	name, desc, tz, rows, err := s.validate(ctx, tenantID, in)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var id uuid.UUID
	err = tx.QueryRowContext(ctx, `
		INSERT INTO oncall_schedules (tenant_id, name, description, timezone) VALUES ($1, $2, $3, $4) RETURNING id
	`, tenantID, name, desc, tz).Scan(&id)
	if isUniqueViolation(err) {
		return nil, Conflict{Message: "a schedule with this name already exists"}
	}
	if err != nil {
		return nil, fmt.Errorf("insert schedule: %w", err)
	}
	if err := insertRotations(ctx, tx, id, rows); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.Get(ctx, tenantID, id)
}

// Replace rewrites a schedule's fields and rotations. Overrides and the
// iCal feed are kept.
func (s *Service) Replace(ctx context.Context, tenantID, id uuid.UUID, in ScheduleInput) (*Schedule, error) {
	name, desc, tz, rows, err := s.validate(ctx, tenantID, in)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `
		UPDATE oncall_schedules SET name = $3, description = $4, timezone = $5, updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID, name, desc, tz)
	if isUniqueViolation(err) {
		return nil, Conflict{Message: "a schedule with this name already exists"}
	}
	if err != nil {
		return nil, fmt.Errorf("update schedule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM oncall_rotations WHERE schedule_id = $1`, id); err != nil {
		return nil, fmt.Errorf("clear rotations: %w", err)
	}
	if err := insertRotations(ctx, tx, id, rows); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.Get(ctx, tenantID, id)
}

// Delete removes a schedule with its rotations, overrides and feed.
func (s *Service) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM oncall_schedules WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// OnCall answers who is on call at an instant (now when at is zero).
func (s *Service) OnCall(ctx context.Context, tenantID, id uuid.UUID, at time.Time) (*OnCallResponse, error) {
	if at.IsZero() {
		at = s.now()
	}
	at = at.UTC()
	l, err := s.loadOne(ctx, tenantID, id, at.Add(-currentWindow), at.Add(currentWindow))
	if err != nil {
		return nil, err
	}
	out := &OnCallResponse{At: at}
	out.OnCall, out.Next = l.current(at)
	return out, nil
}

// Preview lists the shifts over [from, to); the window defaults to two
// weeks from now and is capped at oncall.MaxPreview.
func (s *Service) Preview(ctx context.Context, tenantID, id uuid.UUID, from, to time.Time) (*PreviewResponse, error) {
	if from.IsZero() {
		from = s.now()
	}
	if to.IsZero() {
		to = from.Add(DefaultPreviewSpan)
	}
	from, to = from.UTC(), to.UTC()
	if !to.After(from) {
		return nil, invalid("to must be after from")
	}
	if to.Sub(from) > oncall.MaxPreview {
		return nil, invalid("a preview covers at most %d days", int(oncall.MaxPreview/(24*time.Hour)))
	}
	l, err := s.loadOne(ctx, tenantID, id, from, to)
	if err != nil {
		return nil, err
	}
	out := &PreviewResponse{From: from, To: to, Timezone: l.Timezone, Shifts: []Shift{}}
	for _, sh := range l.sched.Preview(from, to) {
		out.Shifts = append(out.Shifts, l.shift(sh))
	}
	return out, nil
}

// ---- overrides ----

func (s *Service) requireSchedule(ctx context.Context, tenantID, id uuid.UUID) error {
	var ok bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM oncall_schedules WHERE id = $1 AND tenant_id = $2)`,
		id, tenantID).Scan(&ok); err != nil {
		return fmt.Errorf("load schedule: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// ListOverrides returns the overrides that end after from (default now) and,
// when to is set, start before it.
func (s *Service) ListOverrides(ctx context.Context, tenantID, id uuid.UUID, from, to time.Time) ([]Override, error) {
	if err := s.requireSchedule(ctx, tenantID, id); err != nil {
		return nil, err
	}
	if from.IsZero() {
		from = s.now()
	}
	query := `SELECT id, user_id, starts_at, ends_at, note, created_by, created_at
		FROM oncall_overrides WHERE schedule_id = $1 AND ends_at > $2`
	args := []any{id, from}
	if !to.IsZero() {
		query += ` AND starts_at < $3`
		args = append(args, to)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY starts_at, created_at LIMIT 500`, args...)
	if err != nil {
		return nil, fmt.Errorf("list overrides: %w", err)
	}
	defer rows.Close()
	type raw struct {
		o         Override
		user      uuid.UUID
		createdBy *uuid.UUID
	}
	var list []raw
	var userIDs []uuid.UUID
	for rows.Next() {
		var r raw
		if err := rows.Scan(&r.o.ID, &r.user, &r.o.StartsAt, &r.o.EndsAt, &r.o.Note, &r.createdBy, &r.o.CreatedAt); err != nil {
			return nil, err
		}
		userIDs = append(userIDs, r.user)
		if r.createdBy != nil {
			userIDs = append(userIDs, *r.createdBy)
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	refs, err := s.userRefs(ctx, tenantID, userIDs)
	if err != nil {
		return nil, err
	}
	out := make([]Override, 0, len(list))
	for _, r := range list {
		r.o.User = refFor(refs, r.user)
		if r.createdBy != nil {
			by := refFor(refs, *r.createdBy)
			r.o.CreatedBy = &by
		}
		out = append(out, r.o)
	}
	return out, nil
}

// CreateOverride puts a member on call over a window. createdBy is the
// signed-in user (uuid.Nil for an API key).
func (s *Service) CreateOverride(ctx context.Context, tenantID, id, createdBy uuid.UUID, req CreateOverrideRequest) (*Override, error) {
	if err := s.requireSchedule(ctx, tenantID, id); err != nil {
		return nil, err
	}
	if req.UserID == uuid.Nil {
		return nil, invalid("user_id is required")
	}
	if req.StartsAt.IsZero() || req.EndsAt.IsZero() {
		return nil, invalid("starts_at and ends_at are required")
	}
	if !req.EndsAt.After(req.StartsAt) {
		return nil, invalid("ends_at must be after starts_at")
	}
	if !req.EndsAt.After(s.now()) {
		return nil, invalid("ends_at must be in the future")
	}
	if req.EndsAt.Sub(req.StartsAt) > MaxOverrideSpan {
		return nil, invalid("an override lasts at most %d days", int(MaxOverrideSpan/(24*time.Hour)))
	}
	note, err := cleanText("note", req.Note, MaxNoteLength, false)
	if err != nil {
		return nil, err
	}
	if err := s.requireEligible(ctx, tenantID, []uuid.UUID{req.UserID}); err != nil {
		return nil, err
	}
	var by *uuid.UUID
	if createdBy != uuid.Nil {
		by = &createdBy
	}
	var oid uuid.UUID
	if err := s.db.QueryRowContext(ctx, `
		INSERT INTO oncall_overrides (schedule_id, user_id, starts_at, ends_at, note, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id
	`, id, req.UserID, req.StartsAt.UTC(), req.EndsAt.UTC(), note, by).Scan(&oid); err != nil {
		return nil, fmt.Errorf("insert override: %w", err)
	}
	list, err := s.ListOverrides(ctx, tenantID, id, req.StartsAt.Add(-time.Nanosecond), req.EndsAt)
	if err != nil {
		return nil, err
	}
	for _, o := range list {
		if o.ID == oid {
			return &o, nil
		}
	}
	return nil, ErrNotFound
}

// DeleteOverride removes an override.
func (s *Service) DeleteOverride(ctx context.Context, tenantID, id, overrideID uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM oncall_overrides o USING oncall_schedules s
		WHERE o.id = $1 AND o.schedule_id = $2 AND s.id = o.schedule_id AND s.tenant_id = $3
	`, overrideID, id, tenantID)
	if err != nil {
		return fmt.Errorf("delete override: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- iCal feed ----

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// RotateICalToken turns the feed on with a new token, revoking any previous
// one. The token is returned once; only its hash is stored.
func (s *Service) RotateICalToken(ctx context.Context, tenantID, id uuid.UUID) (*ICalToken, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generate feed token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	res, err := s.db.ExecContext(ctx, `UPDATE oncall_schedules SET ical_token_hash = $3, updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2`, id, tenantID, hashToken(token))
	if err != nil {
		return nil, fmt.Errorf("store feed token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	out := &ICalToken{Token: token, Path: FeedPathPrefix + token + ".ics"}
	if s.publicBaseURL != "" {
		out.URL = s.publicBaseURL + out.Path
	}
	return out, nil
}

// DisableICal turns the feed off.
func (s *Service) DisableICal(ctx context.Context, tenantID, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `UPDATE oncall_schedules SET ical_token_hash = NULL, updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("disable feed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Feed renders the iCal feed of the schedule whose token this is: the
// shifts from two weeks back to nine weeks ahead. An unknown token, or one
// of a deleted workspace, is ErrNotFound.
func (s *Service) Feed(ctx context.Context, token string) (string, error) {
	if token == "" || len(token) > 128 {
		return "", ErrNotFound
	}
	var id, tenantID uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.tenant_id FROM oncall_schedules s
		WHERE s.ical_token_hash = $1 AND `+tenancy.LiveTenantSQL("s.tenant_id"), hashToken(token)).Scan(&id, &tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("resolve feed token: %w", err)
	}
	now := s.now().UTC()
	from, to := now.Add(-feedBack), now.Add(feedAhead)
	l, err := s.loadOne(ctx, tenantID, id, from, to)
	if err != nil {
		return "", err
	}
	rotationNames := map[uuid.UUID]string{}
	for _, r := range l.Rotations {
		rotationNames[r.ID] = r.Name
	}
	cal := oncall.Calendar{Name: "On call: " + l.Name, Stamp: now}
	for _, sh := range l.sched.Preview(from, to) {
		ref := refFor(l.refs, sh.UserID)
		desc := "Schedule: " + l.Name
		if sh.Source == oncall.SourceOverride {
			desc += "\nOverride"
		} else if n := rotationNames[sh.RotationID]; n != "" {
			desc += "\nRotation: " + n
		}
		cal.Events = append(cal.Events, oncall.CalendarEvent{
			UID:         fmt.Sprintf("%s-%d-%s@probara", l.ID, sh.Start.Unix(), sh.UserID),
			Start:       sh.Start,
			End:         sh.End,
			Summary:     "On call: " + ref.Username,
			Description: desc,
		})
	}
	return cal.Render(), nil
}
