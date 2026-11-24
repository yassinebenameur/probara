//go:build !unit

package notifications

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A notification queued before its workspace was deleted is dropped: the
// channel reads as gone, so nothing is sent and nothing is recorded.
func TestLoadChannelRefusesDeletedWorkspace(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	c := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}

	channel := func(tenant uuid.UUID) uuid.UUID {
		id := uuid.New()
		if _, err := dbClient.ExecContext(ctx, `
			INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active)
			VALUES ($1, $2, 'hook', 'webhook', '{}'::jsonb, TRUE)`, id, tenant); err != nil {
			t.Fatal(err)
		}
		return id
	}
	live := testutil.InsertTenant(ctx, t, dbClient, "")
	deleted := testutil.InsertTenant(ctx, t, dbClient, "")
	liveChannel, deletedChannel := channel(live), channel(deleted)
	if _, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, deleted); err != nil {
		t.Fatal(err)
	}

	if _, err := c.loadChannel(ctx, liveChannel.String()); err != nil {
		t.Fatalf("live channel: %v", err)
	}
	if _, err := c.loadChannel(ctx, deletedChannel.String()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted workspace channel: err = %v, want sql.ErrNoRows (dropped as gone)", err)
	}
}
