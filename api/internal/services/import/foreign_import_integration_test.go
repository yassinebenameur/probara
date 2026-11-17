//go:build !unit

package importservice

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestExecuteImport_ForeignSourcesEndToEnd imports each source's fixture
// through the preview's own rows and mapping into the real monitor service
// and Postgres: every row is created, each monitor records exactly one
// "import" configuration version, groups gain their junction-table members,
// imported credentials are stored encrypted, and the domain monitor keeps
// its type's rules.
func TestExecuteImport_ForeignSourcesEndToEnd(t *testing.T) {
	ctx := context.Background()
	dbClient, teardown := testutil.SetupPostgresDB(ctx, t)
	defer teardown()

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	t.Setenv("PROBARA_SECRETS_KEY", base64.StdEncoding.EncodeToString(key))
	keys, err := secrets.NewEnvKeyProvider()
	if err != nil {
		t.Fatalf("NewEnvKeyProvider: %v", err)
	}

	var adminID uuid.UUID
	if err := dbClient.QueryRowContext(ctx, `INSERT INTO admin_users (username, password_hash) VALUES ('migrator', 'x') RETURNING id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	session := ctxpkg.WithAdminID(ctx, adminID.String())

	plaintext := map[string][]string{
		"Better Stack": {"bs-secret-token", "bs-basic-pass"},
		"UptimeRobot":  {"ur-secret-pass", "ur-api-key-123"},
		"Gatus":        {"gatus-static-token", "gatus-url-pass"},
	}

	for _, fixture := range loadForeignFixtures(t) {
		t.Run(fixture.source, func(t *testing.T) {
			tenantID := testutil.InsertTenant(ctx, t, dbClient, "import-"+strings.ReplaceAll(strings.ToLower(fixture.source), " ", "-"))
			monitorSvc := monitorservice.NewService(monitorservice.NewPostgresRepository(dbClient))
			monitorSvc.ConfigureEncryption(secrets.NewAESGCMEncryptor(keys))
			svc := NewService(dbClient, monitorSvc, groupservice.NewService(dbClient))

			preview, err := svc.ParseFile(fixture.contents, fixture.upload)
			if err != nil {
				t.Fatalf("ParseFile() error = %v", err)
			}
			result, err := svc.ExecuteImport(session, tenantID, &models.ImportExecuteRequest{
				Rows: preview.Rows, Mapping: preview.SuggestedMapping, TypeMapping: preview.SuggestedTypeMapping,
			})
			if err != nil {
				t.Fatalf("ExecuteImport() error = %v", err)
			}
			for _, row := range result.Results {
				if row.Status != "success" {
					t.Errorf("%s (%s): %s %s%s", row.Name, row.Type, row.Status, row.Error, row.SkipReason)
				}
			}
			if result.SuccessCount != len(preview.Rows) {
				t.Fatalf("created %d of %d rows", result.SuccessCount, len(preview.Rows))
			}

			ids := make(map[string]uuid.UUID, len(result.Results))
			for _, row := range result.Results {
				ids[row.Name] = uuid.MustParse(*row.MonitorID)
			}

			store := monitorSvc.Versions()
			for name, id := range ids {
				list, err := store.List(ctx, tenantID, id, 1, 20)
				if err != nil {
					t.Fatal(err)
				}
				if len(list.Items) != 1 || list.Items[0].Source != monitorservice.VersionSourceImport {
					t.Fatalf("%s: versions = %+v, want one import version", name, list.Items)
				}
			}

			for _, row := range preview.Rows {
				name := fmt.Sprint(row.Fields["name"])
				switch row.Fields["type"] {
				case string(models.MonitorTypeGroup):
					want := make([]string, 0)
					for _, member := range row.Fields["group_members"].([]string) {
						want = append(want, ids[member].String())
					}
					got := groupMembers(ctx, t, dbClient, ids[name])
					sort.Strings(want)
					if strings.Join(got, ",") != strings.Join(want, ",") {
						t.Fatalf("group %s members = %v, want %v", name, got, want)
					}
				case string(models.MonitorTypeDomain):
					var threshold, interval int
					if err := dbClient.QueryRowContext(ctx, `SELECT consecutive_failures_threshold, interval_seconds FROM monitors WHERE id = $1`, ids[name]).
						Scan(&threshold, &interval); err != nil {
						t.Fatal(err)
					}
					if threshold < 2 || interval < 3600 {
						t.Fatalf("domain monitor %s stored threshold %d, interval %d", name, threshold, interval)
					}
				}
			}

			var configs []string
			rows, err := dbClient.QueryContext(ctx, `SELECT config::text FROM monitors WHERE tenant_id = $1`, tenantID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var config string
				if err := rows.Scan(&config); err != nil {
					t.Fatal(err)
				}
				configs = append(configs, config)
			}
			stored := strings.Join(configs, "\n")
			for _, secret := range plaintext[fixture.source] {
				if strings.Contains(stored, secret) {
					t.Fatalf("credential %q was stored in plaintext", secret)
				}
			}
		})
	}
}

// groupMembers reads a group's junction-table rows, sorted.
func groupMembers(ctx context.Context, t *testing.T, dbClient *shareddb.Client, groupID uuid.UUID) []string {
	t.Helper()
	rows, err := dbClient.QueryContext(ctx, `SELECT monitor_id::text FROM monitor_groups WHERE group_id = $1 ORDER BY monitor_id::text`, groupID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	members := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		members = append(members, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return members
}
