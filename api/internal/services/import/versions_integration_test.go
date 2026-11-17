//go:build !unit

package importservice

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestExecuteImport_RecordsOneVersionPerMonitor: both import paths record
// exactly one configuration version per created monitor, with source
// "import" and the importing user, and an imported group's version already
// holds the members attached in the second pass.
func TestExecuteImport_RecordsOneVersionPerMonitor(t *testing.T) {
	ctx := context.Background()
	dbClient, teardown := testutil.SetupPostgresDB(ctx, t)
	defer teardown()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "import-versions")
	var adminID uuid.UUID
	if err := dbClient.QueryRowContext(ctx, `INSERT INTO admin_users (username, password_hash) VALUES ('importer', 'x') RETURNING id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	session := ctxpkg.WithAdminID(ctx, adminID.String())

	monitorSvc := monitorservice.NewService(monitorservice.NewPostgresRepository(dbClient))
	svc := NewService(dbClient, monitorSvc, groupservice.NewService(dbClient))
	store := monitorSvc.Versions()

	raw, err := svc.ExecuteImport(session, tenantID, &models.ImportExecuteRequest{
		Rows: []models.ImportRow{
			rowWithConfig(0, "Web", "http", map[string]interface{}{"url": "https://web.example", "method": "GET", "expected_status": 200}, nil, 60, 30),
			rowWithConfig(1, "Edge", "group", map[string]interface{}{}, []string{"Web"}, 60, 0),
		},
		Mapping: models.FieldMapping{Name: "name", Type: "type", Config: "config", IntervalSeconds: "interval_seconds",
			TimeoutSeconds: "timeout_seconds", GroupMembers: "group_members"},
	})
	if err != nil || raw.SuccessCount != 2 {
		t.Fatalf("raw import = %+v, %v", raw, err)
	}
	simple, err := svc.ExecuteImport(session, tenantID, &models.ImportExecuteRequest{
		Rows:    []models.ImportRow{{Index: 0, Fields: map[string]interface{}{"name": "Docs", "type": "http", "url": "https://docs.example"}}},
		Mapping: models.FieldMapping{Name: "name", Type: "type", URL: "url"},
	})
	if err != nil || simple.SuccessCount != 1 {
		t.Fatalf("simple import = %+v, %v", simple, err)
	}

	byName := map[string]uuid.UUID{}
	for _, result := range append(raw.Results, simple.Results...) {
		byName[result.Name] = uuid.MustParse(*result.MonitorID)
	}
	for name, id := range byName {
		list, err := store.List(ctx, tenantID, id, 1, 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(list.Items) != 1 {
			t.Fatalf("%s: %d versions, want 1", name, len(list.Items))
		}
		v := list.Items[0]
		if v.Source != monitorservice.VersionSourceImport || v.ActorType != monitorservice.VersionActorUser || v.ActorLabel != "importer" {
			t.Fatalf("%s: version = %+v", name, v)
		}
	}
	group, err := store.Get(ctx, tenantID, byName["Edge"], 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(group.Snapshot.MemberIDs, ",") != byName["Web"].String() {
		t.Fatalf("imported group version members = %v", group.Snapshot.MemberIDs)
	}
}
