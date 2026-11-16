//go:build !unit

package monitors

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// countingDB counts the statements recording sends.
type countingDB struct {
	tx *sql.Tx
	n  atomic.Int64
}

func (c *countingDB) QueryContext(ctx context.Context, q string, args ...interface{}) (*sql.Rows, error) {
	c.n.Add(1)
	return c.tx.QueryContext(ctx, q, args...)
}

func (c *countingDB) QueryRowContext(ctx context.Context, q string, args ...interface{}) *sql.Row {
	c.n.Add(1)
	return c.tx.QueryRowContext(ctx, q, args...)
}

func (c *countingDB) ExecContext(ctx context.Context, q string, args ...interface{}) (sql.Result, error) {
	c.n.Add(1)
	return c.tx.ExecContext(ctx, q, args...)
}

// System versions for a change touching hundreds of monitors cost a
// handful of statements, not a dozen per monitor, and a recording failure
// leaves the change itself to commit.
func TestSystemVersions_SetBasedAndNeverFailTheChange_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, db, "system-versions")

	const count = 300
	var locationID uuid.UUID
	if err := db.QueryRowContext(ctx, `INSERT INTO locations (tenant_id, name, slug) VALUES ($1, 'paris', 'paris') RETURNING id`, tenant).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	ids := make([]uuid.UUID, 0, count)
	for i := 0; i < count; i++ {
		id := testutil.InsertHTTPMonitor(ctx, t, db, tenant, uuid.NewString())
		ids = append(ids, id)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO monitor_locations (monitor_id, location_id) SELECT unnest($1::uuid[]), $2`, uuidArray(ids), locationID); err != nil {
		t.Fatal(err)
	}
	// Half the monitors already have history, half get a baseline.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO monitor_config_versions (tenant_id, monitor_id, version, snapshot, actor_type, source)
		SELECT $1, id, 1, '{"name":"old","type":"http","config":{}}', 'system', 'ui' FROM unnest($2::uuid[]) AS id
	`, tenant, uuidArray(ids[:count/2])); err != nil {
		t.Fatal(err)
	}

	detach := func(record bool) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		counting := &countingDB{tx: tx}
		started := time.Now()
		versions := PrepareSystemVersionsTx(ctx, counting, tenant, ids)
		if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_locations WHERE location_id = $1`, locationID); err != nil {
			t.Fatal(err)
		}
		versions.RecordTx(ctx, counting)
		if err := tx.Commit(); err != nil {
			t.Fatalf("the change could not commit after recording: %v", err)
		}
		if record {
			if n := counting.n.Load(); n > 20 {
				t.Fatalf("recording %d monitors took %d statements", count, n)
			}
			if elapsed := time.Since(started); elapsed > 10*time.Second {
				t.Fatalf("recording %d monitors took %s", count, elapsed)
			}
		}
	}
	detach(true)
	var withBaseline, withSystem int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE source = 'baseline'), count(*) FILTER (WHERE source = 'system')
		FROM monitor_config_versions WHERE tenant_id = $1`, tenant).Scan(&withBaseline, &withSystem); err != nil {
		t.Fatal(err)
	}
	var unversioned int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM monitor_config_versions WHERE tenant_id = $1 AND source = 'system' AND NOT ('location_ids' = ANY(changed_fields))
		AND monitor_id = ANY($2)`, tenant, uuidArray(ids[count/2:])).Scan(&unversioned); err != nil || unversioned != 0 {
		t.Fatalf("a baselined monitor's system version does not name location_ids (%d, %v)", unversioned, err)
	}
	if withBaseline != count/2 || withSystem != count {
		t.Fatalf("baselines = %d, system versions = %d, want %d and %d", withBaseline, withSystem, count/2, count)
	}

	// A recording failure: the change still commits, without versions.
	if _, err := db.ExecContext(ctx, `
		CREATE FUNCTION refuse_versions() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'versions refused'; END $$;
		CREATE TRIGGER refuse_versions BEFORE INSERT ON monitor_config_versions FOR EACH ROW EXECUTE FUNCTION refuse_versions();
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO monitor_locations (monitor_id, location_id) SELECT unnest($1::uuid[]), $2`, uuidArray(ids), locationID); err != nil {
		t.Fatal(err)
	}
	detach(false)
	var attached int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM monitor_locations WHERE location_id = $1`, locationID).Scan(&attached)
	if attached != 0 {
		t.Fatalf("the change rolled back with its versions (%d rows left)", attached)
	}
}

func uuidArray(ids []uuid.UUID) any {
	return pq.Array(ids)
}
