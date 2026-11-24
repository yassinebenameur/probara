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
// First it renames keys that were stored miscased before validation refused
// them (`Headers`, `Variables`, `Metadata`, a step's `Request`/`Headers`,
// `Basic_Auth_Password`, …) to their exact spelling, for every type with
// secret fields, merging two spellings when they agree and reporting the
// monitor when they do not (secrets.CanonicalizeCredentialKeys). A credential-named
// variable whose move would create a secret name validation refuses stays
// plain and is reported. Then it applies exactly the function every write
// applies, so the backfill and the write path cannot disagree. Moved values are encrypted with the current
// key; values already in the secret maps keep their envelopes. Soft-deleted
// monitors are migrated too: their rows stay in the database until purged,
// and a credential at rest is one whether or not the monitor is listed. Safe
// to run multiple times: a second run finds nothing to do. Values never
// reach the log, only monitor ids.
//
// It also scrubs monitor configuration history (monitor_config_versions):
// a version recorded from a row like the above, before snapshots applied the
// same rules, can hold such a credential. Each snapshot's config is passed
// through secrets.MaskStoredMonitorConfig, the function versions are taken
// with now, so every credential in it reads "***"; rows already clean are
// left alone, so this part is idempotent too.
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
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/secrets"
)

// migratedTypes are every type with a secret field or a plain credential
// path: NormalizeSensitiveHeaders rewrites http, grpc and the synthetic
// types, and any of them may hold a miscased key.
func migratedTypes() []string {
	set := map[string]bool{}
	for t := range secrets.MonitorSecretFields {
		set[t] = true
	}
	for t := range secrets.MonitorSecretMapFields {
		set[t] = true
	}
	for t := range secrets.PlainCredentialPaths {
		set[t] = true
	}
	types := make([]string, 0, len(set))
	for t := range set {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// stats counts rows. Conflicted rows hold two spellings of one key that
// disagree and are left untouched; Unmovable rows keep a credential-named
// variable in plain text. Both are logged by monitor id for an operator.
type stats struct {
	Processed, Updated, Skipped, Errored, Conflicted, Unmovable int
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
	versions, err := scrubVersions(ctx, db, !*dryRun, log.Printf)
	if err != nil {
		log.Fatalf("scrub versions: %v", err)
	}
	fmt.Printf("\nprocessed=%d updated=%d skipped=%d errored=%d conflicted=%d unmovable=%d versions_scrubbed=%d versions_errored=%d dry_run=%v\n",
		result.Processed, result.Updated, result.Skipped, result.Errored, result.Conflicted, result.Unmovable,
		versions.Scrubbed, versions.Errored, *dryRun)
	if result.Errored > 0 || result.Conflicted > 0 || versions.Errored > 0 {
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
		pq.Array(migratedTypes()))
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

		row, err := migrateConfig(enc, mType, rawJSON)
		if err != nil {
			logf("[err] monitor %s (type=%s): %v", id, mType, err)
			result.Errored++
			continue
		}
		if len(row.conflicts) > 0 {
			// Key paths only, never values.
			logf("[conflict] monitor %s (type=%s): keys spelled in more than one case with different values (%s); fix them by hand, then run again",
				id, mType, strings.Join(row.conflicts, ", "))
			result.Conflicted++
			continue
		}
		if len(row.unmovable) > 0 {
			logf("[warn] monitor %s (type=%s): credential-named variables stay in plain text because moving them would create an invalid secret name (%s); rename them",
				id, mType, strings.Join(row.unmovable, ", "))
			result.Unmovable++
		}
		if !row.changed {
			result.Skipped++
			continue
		}
		updates = append(updates, pending{id: id, mType: mType, original: rawJSON, config: row.config})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, fmt.Errorf("row iter: %w", err)
	}
	rows.Close()

	for _, u := range updates {
		if !apply {
			logf("[dry-run] would move plaintext credentials or rename miscased keys of monitor %s (type=%s)", u.id, u.mType)
			result.Updated++
			continue
		}
		if err := writeRow(ctx, db, u.id, u.original, u.config); err != nil {
			logf("[err] monitor %s: write: %v", u.id, err)
			result.Errored++
			continue
		}
		result.Updated++
		logf("[ok] moved plaintext credentials or renamed miscased keys of monitor %s (type=%s)", u.id, u.mType)
	}
	return result, nil
}

// rowResult is what migrateConfig decided for one row.
type rowResult struct {
	config    json.RawMessage
	changed   bool
	conflicts []string
	unmovable []string
}

// migrateConfig repairs miscased keys, normalizes the config and encrypts
// what moved. Values already in the secret maps keep their envelopes
// (EncryptMonitorConfig passes them through), so a row is only rewritten
// when a key was renamed or a credential actually moved.
func migrateConfig(enc secrets.Encryptor, monitorType string, raw json.RawMessage) (rowResult, error) {
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return rowResult{}, fmt.Errorf("decode config: %w", err)
	}
	renamed, conflicts := secrets.CanonicalizeCredentialKeys(monitorType, cfg)
	if len(conflicts) > 0 {
		return rowResult{conflicts: conflicts}, nil
	}
	canonical := raw
	if renamed {
		out, err := json.Marshal(cfg)
		if err != nil {
			return rowResult{}, fmt.Errorf("encode config: %w", err)
		}
		canonical = out
	}
	normalized, moved, err := secrets.NormalizeSensitiveHeaders(monitorType, canonical)
	if err != nil {
		return rowResult{}, err
	}
	result := rowResult{unmovable: secrets.UnmovableSensitiveVariables(monitorType, normalized)}
	if !renamed && !moved {
		return result, nil
	}
	encrypted, err := secrets.EncryptMonitorConfig(enc, monitorType, normalized)
	if err != nil {
		return rowResult{}, err
	}
	result.config, result.changed = encrypted, true
	return result, nil
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

// versionStats counts configuration history rows scrubbed or failed.
type versionStats struct {
	Scrubbed, Errored int
}

// scrubVersions masks every credential left in a configuration version's
// snapshot (see the package doc). A row is rewritten only when masking
// changes it, and only if it is still the one read. Without apply it only
// counts. A database without the history table has nothing to scrub.
func scrubVersions(ctx context.Context, db *sql.DB, apply bool, logf func(string, ...any)) (versionStats, error) {
	var result versionStats
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('monitor_config_versions') IS NOT NULL`).Scan(&exists); err != nil {
		return result, fmt.Errorf("check history table: %w", err)
	}
	if !exists {
		return result, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT id, monitor_id, version, snapshot FROM monitor_config_versions ORDER BY id`)
	if err != nil {
		return result, fmt.Errorf("scan versions: %w", err)
	}
	type pending struct {
		id        int64
		label     string
		original  []byte
		rewritten []byte
	}
	var updates []pending
	for rows.Next() {
		var (
			id        int64
			monitorID uuid.UUID
			version   int
			raw       []byte
		)
		if err := rows.Scan(&id, &monitorID, &version, &raw); err != nil {
			logf("scan version row: %v", err)
			result.Errored++
			continue
		}
		label := fmt.Sprintf("monitor %s version %d", monitorID, version)
		rewritten, changed, err := scrubSnapshot(raw)
		if err != nil {
			logf("[err] %s: %v", label, err)
			result.Errored++
			continue
		}
		if changed {
			updates = append(updates, pending{id: id, label: label, original: raw, rewritten: rewritten})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, fmt.Errorf("version row iter: %w", err)
	}
	rows.Close()

	for _, u := range updates {
		if !apply {
			logf("[dry-run] would mask credentials in %s", u.label)
			result.Scrubbed++
			continue
		}
		res, err := db.ExecContext(ctx, `UPDATE monitor_config_versions SET snapshot = $1 WHERE id = $2 AND snapshot = $3::jsonb`,
			u.rewritten, u.id, u.original)
		if err != nil {
			logf("[err] %s: write: %v", u.label, err)
			result.Errored++
			continue
		}
		if n, _ := res.RowsAffected(); n != 1 {
			logf("[err] %s: changed or pruned since it was read; run again", u.label)
			result.Errored++
			continue
		}
		result.Scrubbed++
		logf("[ok] masked credentials in %s", u.label)
	}
	return result, nil
}

// scrubSnapshot masks the config of one stored snapshot. changed is false
// when it already held no credential.
func scrubSnapshot(raw []byte) ([]byte, bool, error) {
	var snapshot map[string]any
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, false, fmt.Errorf("decode snapshot: %w", err)
	}
	monitorType, _ := snapshot["type"].(string)
	config, ok := snapshot["config"]
	if !ok || config == nil {
		return raw, false, nil
	}
	configRaw, err := json.Marshal(config)
	if err != nil {
		return nil, false, err
	}
	masked, err := secrets.MaskStoredMonitorConfig(monitorType, configRaw)
	if err != nil {
		return nil, false, err
	}
	var maskedValue any
	if err := json.Unmarshal(masked, &maskedValue); err != nil {
		return nil, false, err
	}
	if reflect.DeepEqual(maskedValue, config) {
		return raw, false, nil
	}
	snapshot["config"] = maskedValue
	out, err := json.Marshal(snapshot)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}
