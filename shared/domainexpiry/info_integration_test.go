//go:build !unit

package domainexpiry

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestSQLExprMatchesExtract pins the two snapshot readers together: for every
// fixture, the object Postgres selects through SQLExpr (what the alerter's
// domain_expiry evaluation reads) must be the object Extract selects in Go,
// and the fields the alerter projects must match the decoded Info.
func TestSQLExprMatchesExtract(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	query := `
		SELECT obj::text, ` + ExpiresAtSQL("obj") + `, ` + UnknownReasonSQL("obj") + `
		FROM (SELECT ` + SQLExpr("d.metrics_data") + ` AS obj
		      FROM (SELECT $1::jsonb AS metrics_data) d) s`

	run := func(t *testing.T, raw json.RawMessage) {
		t.Helper()
		var arg interface{}
		if raw != nil {
			arg = string(raw)
		}
		var sqlObj, expiresAt, unknown sql.NullString
		if err := dbClient.QueryRowContext(ctx, query, arg).Scan(&sqlObj, &expiresAt, &unknown); err != nil {
			t.Fatalf("query: %v", err)
		}
		goObj := ExtractRaw(raw)
		if sqlObj.Valid != (goObj != nil) {
			t.Fatalf("SQL selected %v (%s), Go selected %s", sqlObj.Valid, sqlObj.String, goObj)
		}
		if goObj == nil {
			return
		}
		if !jsonEqual(t, []byte(sqlObj.String), goObj) {
			t.Fatalf("SQL selected %s, Go selected %s", sqlObj.String, goObj)
		}
		info := Extract(raw)
		if info == nil {
			return
		}
		if expiresAt.String != info.ExpiresAt {
			t.Errorf("expires_at: SQL %q, Go %q", expiresAt.String, info.ExpiresAt)
		}
		if unknown.String != info.UnknownReason {
			t.Errorf("unknown_reason: SQL %q, Go %q", unknown.String, info.UnknownReason)
		}
	}

	for _, tt := range pinFixtures {
		t.Run(tt.name, func(t *testing.T) { run(t, json.RawMessage(tt.raw)) })
	}
	t.Run("sql null", func(t *testing.T) { run(t, nil) })
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y interface{}
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("decode %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return bytes.Equal(xa, ya)
}
