// Command migrate_sensitive_headers moves credential-bearing request headers
// that were stored in plaintext monitor config into the encrypted secret maps
// (secrets.NormalizeSensitiveHeaders, the rule every API write applies now):
//
//   - http: a sensitive name in `headers` (Authorization, Cookie, X-API-Key,
//     any *token*/*secret*, …) moves to `secret_headers`.
//   - grpc: a sensitive `metadata` key moves to `secret_metadata`.
//   - synthetic_api: a literal sensitive step header becomes a generated
//     `secret_variables` entry and the header becomes its {{ref}}.
//   - synthetic_api and synthetic_browser: a credential-named plain variable
//     (`password`, `api_token`, …) moves to `secret_variables` under the same
//     name, so its {{ref}}s keep working.
//
// It applies exactly the function every write applies, so the backfill and
// the write path cannot disagree. Moved values are encrypted with the current
// key; values already in the secret maps keep their envelopes. Soft-deleted
// monitors are migrated too: their rows stay in the database until purged,
// and a credential at rest is one whether or not the monitor is listed. Safe
// to run multiple times: a second run finds nothing to do. Values never
// reach the log, only monitor ids.
//
// Usage:
//
//	PROBARA_SECRETS_KEY=<base64> POSTGRES_URL=postgres://... \
//	  go run ./cmd/admin/migrate_sensitive_headers [-dry-run]
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/secrets"
)

// migratedTypes are the types NormalizeSensitiveHeaders rewrites.
var migratedTypes = []string{"http", "grpc", "synthetic_api", "synthetic_browser"}

type stats struct {
	Processed, Updated, Skipped, Errored int
}

func main() {
	dryRun := flag.Bool("dry-run", false, "report what would change without writing")
	flag.Parse()

	dbURL := os.Getenv("POSTGRES_URL")
	if dbURL == "" {
		log.Fatal("POSTGRES_URL is required")
	}
	kp, err := secrets.NewEnvKeyProvider()
	if err != nil {
		log.Fatalf("PROBARA_SECRETS_KEY: %v", err)
	}
	enc := secrets.NewAESGCMEncryptor(kp)

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	result, err := migrate(ctx, db, enc, !*dryRun, log.Printf)
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}
	fmt.Printf("\nprocessed=%d updated=%d skipped=%d errored=%d dry_run=%v\n",
		result.Processed, result.Updated, result.Skipped, result.Errored, *dryRun)
	if result.Errored > 0 {
		os.Exit(1)
	}
}

// migrate walks every monitor of a migrated type, soft-deleted ones included.
// Without apply it only
// counts and logs what would change (by monitor id; values never reach the
// log).
func migrate(ctx context.Context, db *sql.DB, enc secrets.Encryptor, apply bool, logf func(string, ...any)) (stats, error) {
	var result stats
	rows, err := db.QueryContext(ctx,
		`SELECT id, type, config FROM monitors WHERE type = ANY($1) ORDER BY created_at`,
		pq.Array(migratedTypes))
	if err != nil {
		return result, fmt.Errorf("scan monitors: %w", err)
	}
	type pending struct {
		id       uuid.UUID
		mType    string
		original json.RawMessage
		config   json.RawMessage
	}
	var updates []pending
	for rows.Next() {
		var (
			id      uuid.UUID
			mType   string
			rawJSON json.RawMessage
		)
		if err := rows.Scan(&id, &mType, &rawJSON); err != nil {
			logf("scan row: %v", err)
			result.Errored++
			continue
		}
		result.Processed++

		newRaw, changed, err := migrateConfig(enc, mType, rawJSON)
		if err != nil {
			logf("[err] monitor %s (type=%s): %v", id, mType, err)
			result.Errored++
			continue
		}
		if !changed {
			result.Skipped++
			continue
		}
		updates = append(updates, pending{id: id, mType: mType, original: rawJSON, config: newRaw})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, fmt.Errorf("row iter: %w", err)
	}
	rows.Close()

	for _, u := range updates {
		if !apply {
			logf("[dry-run] would move plaintext credentials of monitor %s (type=%s)", u.id, u.mType)
			result.Updated++
			continue
		}
		if err := writeRow(ctx, db, u.id, u.original, u.config); err != nil {
			logf("[err] monitor %s: write: %v", u.id, err)
			result.Errored++
			continue
		}
		result.Updated++
		logf("[ok] moved plaintext credentials of monitor %s (type=%s)", u.id, u.mType)
	}
	return result, nil
}

// migrateConfig normalizes one config and encrypts what moved. Values already
// in the secret maps keep their envelopes (EncryptMonitorConfig passes them
// through), so a row is only rewritten when a header actually moved.
func migrateConfig(enc secrets.Encryptor, monitorType string, raw json.RawMessage) (json.RawMessage, bool, error) {
	normalized, moved, err := secrets.NormalizeSensitiveHeaders(monitorType, raw)
	if err != nil || !moved {
		return raw, false, err
	}
	encrypted, err := secrets.EncryptMonitorConfig(enc, monitorType, normalized)
	if err != nil {
		return nil, false, err
	}
	return encrypted, true, nil
}

// writeRow replaces the config only if it is still the one that was read, so
// an edit made through the API meanwhile is never overwritten; such a row is
// reported and picked up by the next run.
func writeRow(ctx context.Context, db *sql.DB, id uuid.UUID, original, newRaw json.RawMessage) error {
	res, err := db.ExecContext(ctx,
		`UPDATE monitors SET config = $1, updated_at = NOW()
		 WHERE id = $2 AND config = $3::jsonb`,
		[]byte(newRaw), id, []byte(original))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("monitor changed or was purged since it was read; run again")
	}
	return nil
}
