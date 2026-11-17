package monitors

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/dbtx"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// ConfigWrite is one monitor config a config-as-code apply would save:
// MonitorID is nil for a monitor it would create.
type ConfigWrite struct {
	MonitorID *uuid.UUID
	Type      models.MonitorType
	Config    json.RawMessage
}

// ConfigWritePlan is what saving a ConfigWrite would change, worked out
// in-process by the same merge a save runs (mergeConfigForWrite). It never
// holds a secret value: only field names leave this file.
type ConfigWritePlan struct {
	// Masked is the config a version snapshot would record after the save
	// (masked and canonical, so it compares byte for byte with a snapshot).
	Masked json.RawMessage
	// Changed reports a save that would change the stored config, secrets
	// included.
	Changed bool
	// SecretChanges are the secret fields the save would set, replace or
	// clear ("password", "secret_headers.Authorization").
	SecretChanges []string
	// UnverifiedSecrets are secret values the document spells out while the
	// monitor holds one too, left uncompared because the caller may not test
	// saved credentials (compareSecrets false): they count as changes.
	UnverifiedSecrets []string
	// MissingSecrets are secrets the document masks ("***") that the monitor
	// does not hold, so the save leaves them unset.
	MissingSecrets []string
	// Config is what to send to CreateMonitor/UpdateMonitor: the document's
	// config, with a secret value equal to the stored one sent as "***".
	Config json.RawMessage
	// Err is a config the save would refuse (ErrSecretConflict).
	Err error
}

// PlanConfigWrites works out, without writing, what saving each config would
// change. A value the document spells out for a secret the monitor already
// holds is compared with the stored one only when compareSecrets is set:
// comparing answers whether a guess equals a saved credential, which only
// callers that may test saved credentials can ask (as POST /monitors/test
// with a monitor_id requires write access).
func (s *Service) PlanConfigWrites(ctx context.Context, tenantID uuid.UUID, writes []ConfigWrite, compareSecrets bool) ([]ConfigWritePlan, error) {
	stored, err := s.storedConfigs(ctx, tenantID, writes)
	if err != nil {
		return nil, err
	}
	plans := make([]ConfigWritePlan, len(writes))
	for i, write := range writes {
		var existing json.RawMessage
		if write.MonitorID != nil {
			existing = stored[*write.MonitorID]
		}
		plan, err := s.planConfigWrite(write.Type, write.Config, existing, compareSecrets)
		if err != nil {
			return nil, err
		}
		plans[i] = plan
	}
	return plans, nil
}

// storedConfigs reads the stored (encrypted) configs of the existing
// monitors among writes, in one query on the save's transaction when there
// is one.
func (s *Service) storedConfigs(ctx context.Context, tenantID uuid.UUID, writes []ConfigWrite) (map[uuid.UUID]json.RawMessage, error) {
	out := map[uuid.UUID]json.RawMessage{}
	var ids []uuid.UUID
	for _, write := range writes {
		if write.MonitorID != nil {
			ids = append(ids, *write.MonitorID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	if s.versions == nil {
		for _, id := range ids {
			monitor, err := s.repo.GetByID(ctx, tenantID, id)
			if err != nil {
				return nil, err
			}
			out[id] = monitor.Config
		}
		return out, nil
	}
	rows, err := dbtx.Of(ctx, s.versions.db).QueryContext(ctx, `
		SELECT id, config FROM monitors WHERE tenant_id = $1 AND id = ANY($2) AND deleted_at IS NULL
	`, tenantID, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("load monitor configs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var config []byte
		if err := rows.Scan(&id, &config); err != nil {
			return nil, fmt.Errorf("scan monitor config: %w", err)
		}
		out[id] = config
	}
	return out, rows.Err()
}

func (s *Service) planConfigWrite(monitorType models.MonitorType, incoming, existing json.RawMessage, compareSecrets bool) (ConfigWritePlan, error) {
	plan := ConfigWritePlan{Config: incoming, SecretChanges: []string{}, UnverifiedSecrets: []string{}, MissingSecrets: []string{}}
	merged, err := mergeConfigForWrite(monitorType, incoming, existing)
	if err != nil {
		// Only the secret-pair check is the document's to fix; a config that
		// is not a JSON object was refused by validation before.
		plan.Err = err
		return plan, nil
	}
	masked, err := secrets.MaskStoredMonitorConfig(string(monitorType), merged)
	if err != nil {
		return plan, fmt.Errorf("mask planned config: %w", err)
	}
	if plan.Masked, err = canonicalJSON(masked); err != nil {
		return plan, fmt.Errorf("canonicalize planned config: %w", err)
	}

	typ := string(monitorType)
	in := decodeConfigMap(incoming)
	next := decodeConfigMap(merged)
	prev := decodeConfigMap(existing)
	plan.MissingSecrets = missingSecrets(typ, in, next)

	// Secrets field by field: a value carried over from the stored config is
	// byte-identical to it (the merge keeps the stored envelope), anything
	// else came from the document.
	nextEntries, prevEntries := secretEntries(typ, next), secretEntries(typ, prev)
	keep := map[string]bool{}
	for _, path := range unionKeys(nextEntries, prevEntries) {
		after, inNext := nextEntries[path]
		before, inPrev := prevEntries[path]
		switch {
		case inNext && inPrev && after == before:
			// kept
		case inNext && inPrev && !compareSecrets:
			plan.UnverifiedSecrets = append(plan.UnverifiedSecrets, path)
		case inNext && inPrev:
			plain, err := s.encryptor.Decrypt(before)
			if err != nil {
				return plan, fmt.Errorf("decrypt stored %s: %w", path, err)
			}
			if plain == after {
				keep[path] = true
			} else {
				plan.SecretChanges = append(plan.SecretChanges, path)
			}
		default:
			plan.SecretChanges = append(plan.SecretChanges, path)
		}
	}

	plan.Changed = len(plan.SecretChanges) > 0 || len(plan.UnverifiedSecrets) > 0 ||
		!equalJSON(withoutSecretFields(typ, next), withoutSecretFields(typ, prev))
	if len(keep) > 0 {
		// The save gets the document's config with every secret value equal
		// to the stored one sent as "***", so the merge keeps the stored
		// envelope instead of encrypting the same value again as a change.
		// (A credential the document writes in a plain header is moved by
		// the save and re-encrypted; it only shows as a changed secret on a
		// version some other change records.)
		for path := range keep {
			maskSecretEntry(typ, in, path)
		}
		if plan.Config, err = json.Marshal(in); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

// secretEntries lists a config's registry secret values by path: a scalar
// field by name, a map entry as "field.key".
func secretEntries(monitorType string, cfg map[string]any) map[string]string {
	out := map[string]string{}
	for _, field := range secrets.MonitorSecretFields[monitorType] {
		if value, ok := cfg[field].(string); ok && value != "" {
			out[field] = value
		}
	}
	for _, field := range secrets.MonitorSecretMapFields[monitorType] {
		values, _ := cfg[field].(map[string]any)
		for key, value := range values {
			if text, ok := value.(string); ok && text != "" {
				out[field+"."+key] = text
			}
		}
	}
	return out
}

// maskSecretEntry replaces a secret value the document spells out with the
// mask, when the document has it at that path.
func maskSecretEntry(monitorType string, cfg map[string]any, path string) {
	for _, field := range secrets.MonitorSecretFields[monitorType] {
		if path == field {
			if _, ok := cfg[field].(string); ok {
				cfg[field] = secrets.MaskedSecret
			}
			return
		}
	}
	for _, field := range secrets.MonitorSecretMapFields[monitorType] {
		key, ok := strings.CutPrefix(path, field+".")
		if !ok {
			continue
		}
		if values, ok := cfg[field].(map[string]any); ok {
			if _, present := values[key].(string); present {
				values[key] = secrets.MaskedSecret
			}
		}
		return
	}
}

// missingSecrets names the secrets the document masks ("***") that the merge
// could not resolve: the monitor holds none, so the save leaves them unset.
func missingSecrets(monitorType string, incoming, merged map[string]any) []string {
	out := []string{}
	for _, field := range secrets.MonitorSecretFields[monitorType] {
		if incoming[field] == secrets.MaskedSecret {
			if _, ok := merged[field]; !ok {
				out = append(out, field)
			}
		}
	}
	for _, field := range secrets.MonitorSecretMapFields[monitorType] {
		values, _ := incoming[field].(map[string]any)
		kept, _ := merged[field].(map[string]any)
		for key, value := range values {
			if value != secrets.MaskedSecret {
				continue
			}
			if _, ok := lookupKeyFold(kept, key); !ok {
				out = append(out, field+"."+key)
			}
		}
	}
	sort.Strings(out)
	return out
}

// withoutSecretFields is cfg without its registry secret fields.
func withoutSecretFields(monitorType string, cfg map[string]any) map[string]any {
	out := make(map[string]any, len(cfg))
	for key, value := range cfg {
		out[key] = value
	}
	for _, field := range secrets.MonitorSecretFields[monitorType] {
		delete(out, field)
	}
	for _, field := range secrets.MonitorSecretMapFields[monitorType] {
		delete(out, field)
	}
	return out
}

// equalJSON compares two decoded JSON documents by their canonical encoding,
// so number spellings and key order do not count.
func equalJSON(a, b map[string]any) bool {
	encodedA, errA := json.Marshal(a)
	encodedB, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	canonA, errA := canonicalJSON(encodedA)
	canonB, errB := canonicalJSON(encodedB)
	return errA == nil && errB == nil && string(canonA) == string(canonB)
}

func unionKeys(a, b map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]string{a, b} {
		for key := range m {
			if !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
		}
	}
	sort.Strings(out)
	return out
}

// CurrentSnapshots reads the live monitors as version snapshots (masked,
// canonical), keyed by id, in the transaction ctx carries if any. Missing,
// deleted and foreign monitors are absent.
func (v *VersionStore) CurrentSnapshots(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) (map[uuid.UUID]*models.MonitorConfigSnapshot, error) {
	return loadSnapshots(ctx, v.conn(ctx), tenantID, monitorIDs)
}

// LatestVersionNumbers returns the newest kept version number of each
// monitor that has one.
func (v *VersionStore) LatestVersionNumbers(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	out := map[uuid.UUID]int{}
	if len(monitorIDs) == 0 {
		return out, nil
	}
	rows, err := v.conn(ctx).QueryContext(ctx, `
		SELECT monitor_id, max(version) FROM monitor_config_versions
		WHERE tenant_id = $1 AND monitor_id = ANY($2)
		GROUP BY monitor_id
	`, tenantID, pq.Array(monitorIDs))
	if err != nil {
		return nil, fmt.Errorf("load latest monitor versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var version int
		if err := rows.Scan(&id, &version); err != nil {
			return nil, err
		}
		out[id] = version
	}
	return out, rows.Err()
}

// DiffSnapshots lists the paths that differ between two snapshots, the way
// the version diff does (secret paths marked, their values only ever the
// mask), plus a valueless entry for each changed secret field the masks
// cannot show. before may be nil.
func DiffSnapshots(before, after *models.MonitorConfigSnapshot, secretFields []string) ([]models.MonitorVersionChange, error) {
	changes, err := diffSnapshots(before, after)
	if err != nil {
		return nil, err
	}
	return withSecretChanges(changes, before, after, secretFields), nil
}

// NormalizeSnapshot sorts a snapshot's set-valued fields and fills its empty
// ones the way a loaded snapshot has them, so a snapshot built by hand
// compares equal to a loaded one with the same content.
func NormalizeSnapshot(snapshot *models.MonitorConfigSnapshot) {
	normalizeSnapshot(snapshot)
}
