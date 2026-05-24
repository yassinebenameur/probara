// Command encrypt_existing_channels walks every row in alert_channels and
// encrypts secret fields (per the plugin manifest) that are still stored as
// plaintext. Safe to run multiple times — rows already in ciphertext form are
// detected via secrets.LooksLikeEnvelope and left alone.
//
// Usage:
//
//	PROBARA_SECRETS_KEY=<base64-32-bytes> POSTGRES_URL=postgres://... \
//	  go run ./cmd/admin/encrypt_existing_channels [-dry-run]
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
	_ "github.com/lib/pq"

	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
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

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rows, err := db.QueryContext(ctx, `SELECT id, type, config FROM alert_channels ORDER BY created_at`)
	if err != nil {
		log.Fatalf("scan alert_channels: %v", err)
	}
	defer rows.Close()

	var processed, updated, skipped, errored int
	for rows.Next() {
		var (
			id      uuid.UUID
			cType   string
			rawJSON json.RawMessage
		)
		if err := rows.Scan(&id, &cType, &rawJSON); err != nil {
			log.Printf("scan row: %v", err)
			errored++
			continue
		}
		processed++

		p, ok := plugin.DefaultRegistry.Get(cType)
		if !ok {
			log.Printf("[skip] channel %s: unknown plugin type %q", id, cType)
			skipped++
			continue
		}
		manifest := p.Manifest()
		if !hasSecretField(manifest) {
			skipped++
			continue
		}

		cfg := map[string]any{}
		if len(rawJSON) > 0 {
			if err := json.Unmarshal(rawJSON, &cfg); err != nil {
				log.Printf("[err] channel %s: decode config: %v", id, err)
				errored++
				continue
			}
		}

		encrypted, err := secrets.EncryptConfig(enc, manifest, cfg)
		if err != nil {
			log.Printf("[err] channel %s: encrypt: %v", id, err)
			errored++
			continue
		}
		if cfgsEqual(cfg, encrypted) {
			skipped++
			continue
		}

		newRaw, err := json.Marshal(encrypted)
		if err != nil {
			log.Printf("[err] channel %s: re-marshal: %v", id, err)
			errored++
			continue
		}

		if *dryRun {
			log.Printf("[dry-run] would encrypt channel %s (type=%s)", id, cType)
			updated++
			continue
		}

		if err := writeRow(ctx, db, id, newRaw); err != nil {
			log.Printf("[err] channel %s: write: %v", id, err)
			errored++
			continue
		}
		updated++
		log.Printf("[ok] encrypted channel %s (type=%s)", id, cType)
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

func hasSecretField(m plugin.Manifest) bool {
	for _, f := range m.Fields {
		if f.Secret {
			return true
		}
	}
	return false
}

func cfgsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || fmt.Sprintf("%v", va) != fmt.Sprintf("%v", vb) {
			return false
		}
	}
	return true
}

func writeRow(ctx context.Context, db *sql.DB, id uuid.UUID, newRaw json.RawMessage) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `UPDATE alert_channels SET config = $1, updated_at = NOW() WHERE id = $2`, newRaw, id)
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
