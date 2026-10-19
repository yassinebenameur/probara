package api

import (
	"database/sql"
	"testing"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin"
)

// TestMCPCatalogMatchesRouter builds the real router and binds the MCP tool
// catalog to it: every tool's route must exist, and every /api/v1 route must
// be a tool or carry a reason in the catalog's notExposed list.
func TestMCPCatalogMatchesRouter(t *testing.T) {
	sqlDB, err := sql.Open("postgres", "postgres://unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	cfg := &config.APIConfig{}
	cfg.ServiceName = "api-test"
	cfg.NATSURL = "nats://127.0.0.1:1"
	s := NewServer(cfg, logger.New("api-test", "error"), metrics.NewRegistry("api-test"), &db.Client{DB: sqlDB})
	if s.mcpErr != nil {
		t.Fatal(s.mcpErr)
	}
}
