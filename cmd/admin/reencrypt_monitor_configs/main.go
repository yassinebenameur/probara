// Command reencrypt_monitor_configs walks every monitor whose type carries
// secret config fields (see secrets.MonitorSecretFields) and (re-)encrypts
// them with the current key:
//
//   - plaintext values (written before encryption was configured) are encrypted
//   - envelopes from older key versions are decrypted and re-encrypted
//   - envelopes already at the current version are left alone
//
// Safe to run multiple times. For key rotation, run this after deploying the
// new PROBARA_SECRETS_KEY_V<n> so the retired key can be removed.
//
// Usage:
//
//	PROBARA_SECRETS_KEY=<base64> [PROBARA_SECRETS_KEY_V2=<base64> …] \
//	  POSTGRES_URL=postgres://... go run ./cmd/admin/reencrypt_monitor_configs [-dry-run]
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
	_, currentVersion, err := kp.CurrentKey()
	if err != nil {
		log.Fatalf("current key: %v", err)
	}
	log.Printf("re-encrypting with key version %d", currentVersion)

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	secretTypes := make([]string, 0, len(secrets.MonitorSecretFields))
	for t := range secrets.MonitorSecretFields {
		secretTypes = append(secretTypes, t)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rows, err := db.QueryContext(ctx,
		`SELECT id, type, config FROM monitors WHERE type = ANY($1) AND deleted_at IS NULL ORDER BY created_at`,
		pq.Array(secretTypes))
	if err != nil {
		log.Fatalf("scan monitors: %v", err)
	}
	defer rows.Close()

	var processed, updated, skipped, errored int
	for rows.Next() {
		var (
			id      uuid.UUID
			mType   string
			rawJSON json.RawMessage
		)
		if err := rows.Scan(&id, &mType, &rawJSON); err != nil {
			log.Printf("scan row: %v", err)
			errored++
			continue
		}
		processed++

		newRaw, changed, err := reencryptConfig(enc, mType, rawJSON, currentVersion)
		if err != nil {
			log.Printf("[err] monitor %s (type=%s): %v", id, mType, err)
			errored++
			continue
		}
		if !changed {
			skipped++
			continue
		}

		if *dryRun {
			log.Printf("[dry-run] would re-encrypt monitor %s (type=%s)", id, mType)
			updated++
			continue
		}

		if err := writeRow(ctx, db, id, newRaw); err != nil {
			log.Printf("[err] monitor %s: write: %v", id, err)
			errored++
			continue
		}
		updated++
		log.Printf("[ok] re-encrypted monitor %s (type=%s)", id, mType)
	}
	if err := rows.Err(); err != nil {
		log.Fatalf("row iter: %v", err)
	}

	fmt.Printf("\nprocessed=%d updated=%d skipped=%d errored=%d dry_run=%v\n",
		processed, updated, skipped, errored, *dryRun)
	if errored > 0 {
		os.Exit(1)
	}
}

// reencryptConfig brings every secret field of one config to the current key
// version. Returns the updated JSON and whether anything changed.
func reencryptConfig(enc secrets.Encryptor, monitorType string, raw json.RawMessage, currentVersion int) (json.RawMessage, bool, error) {
	cfg := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, false, fmt.Errorf("decode config: %w", err)
		}
	}

	changed := false
	for _, field := range secrets.MonitorSecretFields[monitorType] {
		v, ok := cfg[field]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if version, isEnvelope := secrets.EnvelopeVersion(s); isEnvelope && version == currentVersion {
			continue
		}
		plain, err := enc.Decrypt(s)
		if err != nil {
			return nil, false, fmt.Errorf("decrypt field %q: %w", field, err)
		}
		ct, err := enc.Encrypt(plain)
		if err != nil {
			return nil, false, fmt.Errorf("encrypt field %q: %w", field, err)
		}
		cfg[field] = ct
		changed = true
	}

	if !changed {
		return raw, false, nil
	}
	newRaw, err := json.Marshal(cfg)
	if err != nil {
		return nil, false, fmt.Errorf("re-marshal: %w", err)
	}
	return newRaw, true, nil
}

func writeRow(ctx context.Context, db *sql.DB, id uuid.UUID, newRaw json.RawMessage) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `UPDATE monitors SET config = $1, updated_at = NOW() WHERE id = $2`, []byte(newRaw), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("expected 1 row updated")
	}
	return tx.Commit()
}
