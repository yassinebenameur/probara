//go:build !unit

package scheduler

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// The scrub step leaves no credential in a deleted workspace's monitor
// configs beyond the registry's secret keys either: URL userinfo passwords
// and credential query parameters (top level and nested) are masked, the
// plain twin maps (headers, metadata, variables) and synthetic API step
// header maps (any key case) go whole, nothing is
// deleted, another workspace is untouched, and a second scrub changes
// nothing.
func TestWorkspacePurge_ScrubsURLCredentialsAndPlainMaps(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	const configs = `
		('http',          '{"url":"https://ops:url-pass-1@api.example.com/v1?api_key=query-key-1&page=2","headers":{"X-Custom":"plain-header-1"}}'),
		('websocket',     '{"url":"wss://ops:url-pass-2@ws.example.com/feed"}'),
		('prometheus',    '{"url":"https://scrape:url-pass-3@prom.example.com","query":"up"}'),
		('grpc',          '{"host":"grpc.example.com","port":443,"metadata":{"x-tenant":"plain-meta-1"}}'),
		('synthetic_api', '{"base_url":"https://bot:url-pass-4@flow.example.com","variables":{"region":"plain-var-1"},"steps":[{"name":"s","request":{"method":"GET","url":"https://step:url-pass-5@flow.example.com/x","headers":{"X-Session":"step-header-1"}}},{"name":"t","Request":{"method":"GET","url":"https://flow.example.com/y","Headers":{"X-Trace":"step-header-2"}}}]}'),
		('http',          '{"url":"https://plain.example.com/health"}')`
	seed := func(tenant uuid.UUID) {
		_, err := dbClient.ExecContext(ctx, `
			INSERT INTO monitors (tenant_id, name, type, config, interval_seconds, timeout_seconds)
			SELECT $1, 'scrub-' || c.type || '-' || left(md5(c.config), 8), c.type, c.config::jsonb, 60, 30
			FROM (VALUES `+configs+`) AS c(type, config)`, tenant)
		require.NoError(t, err)
	}
	victim := testutil.InsertTenant(ctx, t, dbClient, "")
	bystander := testutil.InsertTenant(ctx, t, dbClient, "")
	seed(victim)
	seed(bystander)
	markDeleted(ctx, t, dbClient, victim)

	leaked := []string{
		"url-pass-1", "query-key-1", "plain-header-1", "url-pass-2", "url-pass-3",
		"plain-meta-1", "url-pass-4", "plain-var-1", "url-pass-5", "step-header-1", "step-header-2",
	}
	leftIn := func(tenant uuid.UUID) []string {
		var text string
		require.NoError(t, dbClient.QueryRowContext(ctx,
			`SELECT COALESCE(string_agg(config::text, ' '), '') FROM monitors WHERE tenant_id = $1`, tenant).Scan(&text))
		var found []string
		for _, value := range leaked {
			if strings.Contains(text, value) {
				found = append(found, value)
			}
		}
		return found
	}
	require.Len(t, leftIn(victim), len(leaked), "fixture holds every credential")

	p := newWorkspacePurger(dbClient, testLogger(t), nil, purgerOptions{BatchSize: 2, MaxRowsPerRun: 1_000_000})
	p.pause = 0
	scrub := func() int {
		conn, err := dbClient.DB.Conn(ctx)
		require.NoError(t, err)
		defer conn.Close()
		total := 0
		for _, step := range workspaceScrubPlan() {
			n, drained, err := p.runStep(ctx, conn, step, victim, 1_000_000)
			require.NoErrorf(t, err, "scrub step %s", step.table)
			require.Truef(t, drained, "scrub step %s did not finish", step.table)
			total += n
		}
		return total
	}
	require.NotZero(t, scrub())
	require.Empty(t, leftIn(victim), "credentials left after the scrub")
	require.Len(t, leftIn(bystander), len(leaked), "another workspace's monitors were scrubbed")
	require.Zero(t, scrub(), "a second scrub changed rows")

	// The rest of each config stays (the rows go late, in plan order), and
	// the URLs keep everything but their credentials.
	var url, host string
	require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT config->>'url' FROM monitors WHERE tenant_id = $1 AND type = 'http' AND config->>'url' LIKE '%api.example.com%'`, victim).Scan(&url))
	require.Equal(t, "https://ops:***@api.example.com/v1?api_key=***&page=2", url)
	require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT config->>'host' FROM monitors WHERE tenant_id = $1 AND type = 'grpc'`, victim).Scan(&host))
	require.Equal(t, "grpc.example.com", host)
	require.Equal(t, 6, countMonitors(ctx, t, dbClient, victim), "the scrub deletes nothing")

	// A finished workspace costs one query that selects nothing, and a
	// config with nothing to scrub is never rewritten.
	var pending, marked int
	require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitors WHERE tenant_id = $1 AND `+nestedScrubCandidateSQL, victim).Scan(&pending))
	require.Zero(t, pending)
	require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitors WHERE tenant_id = $1 AND config ? $2`, victim, workspacePurgeScrubbedKey).Scan(&marked))
	require.Equal(t, 4, marked, "only the configs with URL credentials or step headers are rewritten")
	var steps string
	require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT config->>'steps' FROM monitors WHERE tenant_id = $1 AND type = 'synthetic_api'`, victim).Scan(&steps))
	require.NotContains(t, strings.ToLower(steps), `"headers"`, "step header maps go whole")
	require.Contains(t, steps, "flow.example.com/y", "the rest of each step stays")
}

func countMonitors(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenant uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitors WHERE tenant_id = $1`, tenant).Scan(&n))
	return n
}
