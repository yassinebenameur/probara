// Command migrate_sensitive_headers moves credential-bearing request headers
// that were stored in plaintext monitor config into the encrypted secret maps
// (secrets.NormalizeSensitiveHeaders, the rule every API write applies now):
//
//   - http: a sensitive name in `headers` (Authorization, Cookie, X-API-Key,
//     any *token*/*secret*, …) moves to `secret_headers`.
//   - synthetic_api: a literal sensitive step header becomes a generated
//     `secret_variables` entry and the header becomes its {{ref}}.
//
// Moved values are encrypted with the current key. Deleted monitors are
// skipped. It reports by default and writes only with -apply; running it
// again finds nothing to do.
//
// Usage:
//
//	PROBARA_SECRETS_KEY=<base64> POSTGRES_URL=postgres://... \
//	  go run ./cmd/admin/migrate_sensitive_headers [-apply]
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
var migratedTypes = []string{"http", "synthetic_api"}

type stats struct {
	Processed, Updated, Skipped, Errored int
}

func main() {
	apply := flag.Bool("apply", false, "write the changes (default: dry run, report only)")
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

	result, err := migrate(ctx, db, enc, *apply, log.Printf)
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}
	fmt.Printf("\nprocessed=%d updated=%d skipped=%d errored=%d dry_run=%v\n",
		result.Processed, result.Updated, result.Skipped, result.Errored, !*apply)
	if result.Errored > 0 {
		os.Exit(1)
	}
}

// migrate walks every live monitor of a migrated type. Without apply it only
// counts and logs what would change (by monitor id; values never reach the
// log).
func migrate(ctx context.Context, db *sql.DB, enc secrets.Encryptor, apply bool, logf func(string, ...any)) (stats, error) {
	var result stats
	rows, err := db.QueryContext(ctx,
		`SELECT id, type, config FROM monitors WHERE type = ANY($1) AND deleted_at IS NULL ORDER BY created_at`,
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
			logf("[dry-run] would move sensitive headers of monitor %s (type=%s)", u.id, u.mType)
			result.Updated++
			continue
		}
		if err := writeRow(ctx, db, u.id, u.original, u.config); err != nil {
			logf("[err] monitor %s: write: %v", u.id, err)
			result.Errored++
			continue
		}
		result.Updated++
		logf("[ok] moved sensitive headers of monitor %s (type=%s)", u.id, u.mType)
	}
	return result, nil
}

// migrateConfig normalizes one config and encrypts what moved. Values already
// in the secret maps keep their envelopes (EncryptMonitorConfig passes them
// through), so a row is only rewritten when a header actually moved.
func migrateConfig(enc secrets.Encryptor, monitorType string, raw json.RawMessage) (json.RawMessage, bool, error) {
	normalized, changed, err := secrets.NormalizeSensitiveHeaders(monitorType, raw)
	if err != nil || !changed {
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
		 WHERE id = $2 AND deleted_at IS NULL AND config = $3::jsonb`,
		[]byte(newRaw), id, []byte(original))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("monitor changed or was deleted since it was read; run again")
	}
	return nil
}
