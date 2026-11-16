//go:build !unit

package monitors_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertchannelservice "github.com/yassinebenameur/probara/api/internal/services/alertchannels"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	locationservice "github.com/yassinebenameur/probara/api/internal/services/locations"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type versionRow struct {
	version int
	source  string
	actor   string
	changed string
}

func versionRows(t *testing.T, ctx context.Context, db *shareddb.Client, monitorID uuid.UUID) []versionRow {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT version, source, actor_type, array_to_string(changed_fields, ',')
		FROM monitor_config_versions WHERE monitor_id = $1 ORDER BY version`, monitorID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []versionRow
	for rows.Next() {
		var r versionRow
		if err := rows.Scan(&r.version, &r.source, &r.actor, &r.changed); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// A monitor that predates history gets its pre-change configuration as a
// baseline on its first save; a pause never records; changes made outside a
// save (a deleted location, channel or member monitor) are recorded as
// system versions instead of being credited to the next save; and a save
// that fails rolls its version back with it.
func TestMonitorConfigVersions_BaselineCascadesAndAtomicity_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenant := testutil.InsertTenant(ctx, t, db, "versions-cascade")
	var adminID uuid.UUID
	if err := db.QueryRowContext(ctx, `INSERT INTO admin_users (username, password_hash) VALUES ('bob', 'x') RETURNING id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	session := ctxpkg.WithAdminID(ctx, adminID.String())
	svc := monitorservice.NewService(monitorservice.NewPostgresRepository(db))
	store := svc.Versions()

	// Baseline: a monitor stored before history existed.
	legacy := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "legacy")
	name := "legacy renamed"
	if _, err := svc.UpdateMonitor(session, tenant, legacy, &models.UpdateMonitorRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	rows := versionRows(t, ctx, db, legacy)
	if len(rows) != 2 || rows[0] != (versionRow{1, "baseline", "system", ""}) || rows[1] != (versionRow{2, "ui", "user", "name"}) {
		t.Fatalf("baseline versions = %+v", rows)
	}
	baseline, err := store.Get(ctx, tenant, legacy, 1)
	if err != nil || baseline.Snapshot.Name != "legacy" {
		t.Fatalf("baseline = %+v, %v", baseline, err)
	}
	// A monitor created now starts at its creation, without a baseline.
	created, err := svc.CreateMonitor(session, tenant, &models.CreateMonitorRequest{
		Name: "fresh", Type: models.MonitorTypeHTTP, IntervalSeconds: 60, TimeoutSeconds: 10,
		Config: json.RawMessage(`{"url":"https://f.example","method":"GET","expected_status":200}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows := versionRows(t, ctx, db, created.ID); len(rows) != 1 || rows[0].source != "ui" {
		t.Fatalf("creation versions = %+v", rows)
	}
	// A legacy monitor that is only paused gets no baseline either.
	paused := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "paused")
	off := false
	if _, err := svc.UpdateMonitor(session, tenant, paused, &models.UpdateMonitorRequest{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if rows := versionRows(t, ctx, db, paused); len(rows) != 0 {
		t.Fatalf("pause recorded %+v", rows)
	}

	// Location delete: a system version, so a later pause stays unrecorded
	// and the next save is not blamed for the location.
	locations := locationservice.NewService(db)
	location, err := locations.Create(ctx, tenant, &models.CreateLocationRequest{Name: "paris"})
	if err != nil {
		t.Fatal(err)
	}
	locationIDs := []string{location.ID.String()}
	if _, err := svc.UpdateMonitor(session, tenant, created.ID, &models.UpdateMonitorRequest{LocationIDs: &locationIDs}); err != nil {
		t.Fatal(err)
	}
	if _, err := locations.Delete(session, tenant, location.ID); err != nil {
		t.Fatal(err)
	}
	rows = versionRows(t, ctx, db, created.ID)
	if last := rows[len(rows)-1]; last.source != "system" || last.actor != "system" || last.changed != "location_ids" {
		t.Fatalf("location delete version = %+v", rows)
	}
	before := len(rows)
	if _, err := svc.UpdateMonitor(session, tenant, created.ID, &models.UpdateMonitorRequest{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if rows := versionRows(t, ctx, db, created.ID); len(rows) != before {
		t.Fatalf("a pause after a cascade recorded %+v", rows[before:])
	}

	// Channel delete.
	var channelID uuid.UUID
	if err := db.QueryRowContext(ctx, `INSERT INTO alert_channels (tenant_id, name, type, config) VALUES ($1, 'ops', 'webhook', '{}') RETURNING id`, tenant).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	mode := "custom"
	if _, err := svc.UpdateMonitor(session, tenant, created.ID, &models.UpdateMonitorRequest{
		NotificationMode: &mode, NotificationChannels: []models.MonitorChannelAssignment{{ChannelID: channelID.String()}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := alertchannelservice.NewService(db, secrets.NoOpEncryptor{}).DeleteAlertChannel(session, tenant, channelID); err != nil {
		t.Fatal(err)
	}
	rows = versionRows(t, ctx, db, created.ID)
	if last := rows[len(rows)-1]; last.source != "system" || last.changed != "notification_channels" {
		t.Fatalf("channel delete version = %+v", rows)
	}

	// Member and dependency delete: the group and the dependent each get a
	// system version.
	group := testutil.InsertGroupMonitor(ctx, t, db, tenant, "edge")
	if err := groupservice.NewService(db).AddMonitorsToGroup(ctx, tenant, group, []uuid.UUID{paused}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO monitor_dependencies (monitor_id, depends_on_id) VALUES ($1, $2)`, legacy, paused); err != nil {
		t.Fatal(err)
	}
	svc.RecordVersion(session, tenant, legacy)
	if err := svc.DeleteMonitor(session, tenant, paused); err != nil {
		t.Fatal(err)
	}
	groupRows := versionRows(t, ctx, db, group)
	if len(groupRows) != 2 || groupRows[0].source != "baseline" || groupRows[1] != (versionRow{2, "system", "system", "member_ids"}) {
		t.Fatalf("group versions after member delete = %+v", groupRows)
	}
	legacyRows := versionRows(t, ctx, db, legacy)
	if last := legacyRows[len(legacyRows)-1]; last.source != "system" || last.changed != "depends_on_ids" {
		t.Fatalf("dependent versions after delete = %+v", legacyRows)
	}

	// Atomicity: a save that fails after the update rolls the update and
	// its version back together.
	count := len(versionRows(t, ctx, db, created.ID))
	failed := errors.New("later step failed")
	newName := "never saved"
	err = svc.InTx(session, func(ctx context.Context) error {
		if _, err := svc.UpdateMonitor(ctx, tenant, created.ID, &models.UpdateMonitorRequest{Name: &newName}); err != nil {
			return err
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("InTx = %v", err)
	}
	monitor, _ := svc.GetMonitor(ctx, tenant, created.ID)
	if monitor.Name == newName || len(versionRows(t, ctx, db, created.ID)) != count {
		t.Fatalf("a failed save left its update or version behind (name %q)", monitor.Name)
	}

	// Bulk edits are one save: a refused monitor leaves every other alone.
	if _, err := svc.BulkUpdateAlerting(session, tenant, []uuid.UUID{created.ID, legacy}, nil, &mode,
		[]models.MonitorChannelAssignment{{ChannelID: uuid.NewString()}}); err == nil || !strings.Contains(err.Error(), "channel") {
		t.Fatalf("bulk edit with a missing channel = %v", err)
	}
	if len(versionRows(t, ctx, db, created.ID)) != count {
		t.Fatal("a failed bulk edit recorded a version")
	}
}
