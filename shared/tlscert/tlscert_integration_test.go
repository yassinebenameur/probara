package tlscert

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestSQLExprMatchesExtract pins the two certificate readers together: for
// every fixture, the object Postgres selects through SQLExpr (what the
// alerter's tls_expiry evaluation reads) must be the object Extract selects
// in Go (what the status page reads), and the fields the alerter projects
// must match the decoded Info.
func TestSQLExprMatchesExtract(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	query := `
		SELECT tls::text, tls #>> '{not_after}', tls #>> '{days_until_expiry}'
		FROM (SELECT ` + SQLExpr("d.metrics_data") + ` AS tls
		      FROM (SELECT $1::jsonb AS metrics_data) d) s`

	run := func(t *testing.T, raw json.RawMessage) {
		t.Helper()
		var arg interface{}
		if raw != nil {
			arg = string(raw)
		}
		var sqlTLS, notAfter, days sql.NullString
		if err := dbClient.QueryRowContext(ctx, query, arg).Scan(&sqlTLS, &notAfter, &days); err != nil {
			t.Fatalf("query: %v", err)
		}
		goTLS := ExtractRaw(raw)
		if sqlTLS.Valid != (goTLS != nil) {
			t.Fatalf("SQL selected %v (%s), Go selected %s", sqlTLS.Valid, sqlTLS.String, goTLS)
		}
		if goTLS == nil {
			return
		}
		if !jsonEqual(t, []byte(sqlTLS.String), goTLS) {
			t.Fatalf("SQL selected %s, Go selected %s", sqlTLS.String, goTLS)
		}
		info := Extract(raw)
		if info == nil {
			return // field decoding is Go-side leniency, not location
		}
		if notAfter.String != info.NotAfter {
			t.Errorf("not_after: SQL %q, Go %q", notAfter.String, info.NotAfter)
		}
		var goDays string
		if info.DaysUntilExpiry != nil {
			goDays = jsonNumber(*info.DaysUntilExpiry)
		}
		if days.String != goDays {
			t.Errorf("days_until_expiry: SQL %q, Go %q", days.String, goDays)
		}
	}

	for _, tt := range pinFixtures {
		t.Run(tt.name, func(t *testing.T) { run(t, json.RawMessage(tt.raw)) })
	}
	t.Run("sql null", func(t *testing.T) { run(t, nil) })
}

func jsonNumber(n int) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}
