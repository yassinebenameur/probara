package discovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/db/pgerr"
	contract "github.com/yassinebenameur/probara/shared/discovery"
)

type ApproveRequest struct {
	NotificationMode string      `json:"notification_mode"`
	ChannelIDs       []uuid.UUID `json:"channel_ids"`
	Key              string      `json:"key"`
	Name             string      `json:"name"`
	IntervalSeconds  int         `json:"interval_seconds"`
	ExpectedStatus   int         `json:"expected_status"`
	LocationID       *uuid.UUID  `json:"location_id"`
}

// LinkRequest is the body of POST /discovery/services/{id}/links. Key, when
// set, binds the link to one current suggestion of the service.
type LinkRequest struct {
	MonitorID uuid.UUID `json:"monitor_id"`
	Key       *string   `json:"key"`
}

// lockService serializes approval/linking against snapshot reconciliation and
// namespace edits, and reports whether the inventory is stale, aged on the DB
// clock. A service outside the integration's active namespaces (removed, its
// inventory awaiting deletion) is not found: Approve, including its
// idempotent replay, and Link refuse it, as Overview no longer lists it.
func (s *Service) lockService(ctx context.Context, tx *sql.Tx, tenant, service uuid.UUID) (bool, error) {
	var stale bool
	err := tx.QueryRowContext(ctx, `SELECT i.last_seen_at IS NULL OR i.last_seen_at<NOW()-`+sqlInterval(InventoryStaleAfter)+` FROM discovery_integrations i JOIN discovered_services s ON s.integration_id=i.id AND s.tenant_id=i.tenant_id WHERE s.id=$1 AND s.tenant_id=$2 AND s.namespace=ANY(i.namespaces) FOR UPDATE OF i`, service, tenant).Scan(&stale)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return stale, err
}

// Approve creates a monitor from one current suggestion of the service and
// links it. created is false when the suggestion was already linked to a live
// monitor (a retry, a double click, or an earlier manual link): that monitor
// is returned and nothing is written.
func (s *Service) Approve(ctx context.Context, tenant, service uuid.UUID, req ApproveRequest) (monitorID uuid.UUID, created bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback()
	// Serialize discovery approvals across integrations in the same workspace,
	// so two independently discovered copies cannot both create the same target.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "discovery-approval:"+tenant.String()); err != nil {
		return uuid.Nil, false, err
	}
	stale, err := s.lockService(ctx, tx, tenant, service)
	if err != nil {
		return uuid.Nil, false, err
	}
	// Idempotency survives retries and also preserves an existing manual link.
	var existing uuid.UUID
	err = tx.QueryRowContext(ctx, `SELECT m.id FROM discovery_monitor_links l JOIN monitors m ON m.id=l.monitor_id AND m.tenant_id=l.tenant_id WHERE l.tenant_id=$1 AND l.service_id=$2 AND l.suggestion_key=$3 AND m.deleted_at IS NULL`, tenant, service, req.Key).Scan(&existing)
	if err == nil {
		return existing, false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, err
	}
	if stale {
		return uuid.Nil, false, invalid("inventory is stale; reconnect the collector before approving")
	}
	rows, err := tx.QueryContext(ctx, `SELECT suggestions FROM discovered_resources WHERE service_id=$1 AND missing_since IS NULL`, service)
	if err != nil {
		return uuid.Nil, false, err
	}
	var suggestion *contract.Suggestion
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return uuid.Nil, false, err
		}
		var ps []contract.Suggestion
		if err = json.Unmarshal(raw, &ps); err != nil {
			rows.Close()
			return uuid.Nil, false, err
		}
		for _, p := range ps {
			if p.Key == req.Key {
				copy := p
				suggestion = &copy
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return uuid.Nil, false, err
	}
	if suggestion == nil {
		return uuid.Nil, false, ErrNotFound
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 200 || req.IntervalSeconds < 30 || req.IntervalSeconds > 86400 {
		return uuid.Nil, false, invalid("provide a name and an interval between 30 and 86400 seconds")
	}
	if suggestion.Internal && req.LocationID == nil {
		return uuid.Nil, false, invalid("internal service checks require a private location")
	}
	if req.LocationID != nil {
		var ok bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM locations WHERE id=$1 AND tenant_id=$2 AND enabled AND deleted_at IS NULL)`, req.LocationID, tenant).Scan(&ok)
		if err != nil {
			return uuid.Nil, false, err
		}
		if !ok {
			return uuid.Nil, false, invalid("choose an enabled location in this workspace")
		}
	}
	// Refuse an exact existing target/location match. The operator can explicitly
	// link it or choose a different vantage point, avoiding accidental duplicates.
	var duplicate bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM monitors m WHERE m.tenant_id=$1 AND m.deleted_at IS NULL AND `+targetMatchSQL("$2", "$3::text[]", "$4")+` AND (($5::uuid IS NULL AND NOT EXISTS(SELECT 1 FROM monitor_locations ml WHERE ml.monitor_id=m.id)) OR EXISTS(SELECT 1 FROM monitor_locations ml WHERE ml.monitor_id=m.id AND ml.location_id=$5)))`, tenant, suggestion.Type, pq.Array(targetForms(*suggestion)), strconv.Itoa(suggestion.Port), req.LocationID).Scan(&duplicate)
	if err != nil {
		return uuid.Nil, false, err
	}
	if duplicate {
		return uuid.Nil, false, invalid("an existing monitor checks this target from this location; link it instead")
	}
	if req.NotificationMode == "" {
		req.NotificationMode = "default"
	}
	if req.NotificationMode != "default" && req.NotificationMode != "custom" {
		return uuid.Nil, false, invalid("invalid notification mode")
	}
	if len(req.ChannelIDs) > 20 || (req.NotificationMode == "default" && len(req.ChannelIDs) > 0) || (req.NotificationMode == "custom" && len(req.ChannelIDs) == 0) {
		return uuid.Nil, false, invalid("select 1–20 active channels for custom routing")
	}
	selectedChannels := map[uuid.UUID]bool{}
	for _, channel := range req.ChannelIDs {
		if selectedChannels[channel] {
			return uuid.Nil, false, invalid("duplicate channel")
		}
		selectedChannels[channel] = true
		var verified uuid.UUID
		err = tx.QueryRowContext(ctx, `SELECT id FROM alert_channels WHERE id=$1 AND tenant_id=$2 AND is_active FOR SHARE`, channel, tenant).Scan(&verified)
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.Nil, false, invalid("select active channels in this workspace")
		}
		if err != nil {
			return uuid.Nil, false, err
		}
	}
	config := map[string]any{"host": suggestion.Target, "port": suggestion.Port}
	if suggestion.Type == "http" {
		if req.ExpectedStatus < 100 || req.ExpectedStatus > 599 {
			return uuid.Nil, false, invalid("expected HTTP status must be between 100 and 599")
		}
		config = map[string]any{"url": suggestion.Target, "method": "GET", "expected_status": req.ExpectedStatus}
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return uuid.Nil, false, err
	}
	if err = validation.DefaultRegistry.Validate(models.MonitorType(suggestion.Type), raw); err != nil {
		return uuid.Nil, false, invalid(err.Error())
	}
	now := time.Now()
	m := &models.Monitor{ID: uuid.New(), TenantID: tenant, Name: req.Name, Type: models.MonitorType(suggestion.Type), Config: raw, IntervalSeconds: req.IntervalSeconds, TimeoutSeconds: 10, Enabled: true, Tags: []string{"discovered"}, NextRunAt: &now, CreatedAt: now, UpdatedAt: now, ConsecutiveFailuresThreshold: 2, NotificationMode: req.NotificationMode, MemberAlertRollup: "per_monitor", DependencySuppression: "inherit", LocationQuorum: 1}
	if err = monitors.NewPostgresRepository(s.db).CreateInTx(ctx, tx, m); err != nil {
		return uuid.Nil, false, err
	}
	if req.LocationID != nil {
		if _, err = tx.ExecContext(ctx, `INSERT INTO monitor_locations(monitor_id,location_id) VALUES($1,$2)`, m.ID, req.LocationID); err != nil {
			return uuid.Nil, false, err
		}
	}
	for _, channel := range req.ChannelIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO monitor_channels(monitor_id,channel_id) VALUES($1,$2)`, m.ID, channel); err != nil {
			return uuid.Nil, false, err
		}
	}
	// Remove soft-deleted bindings, keeping all actual monitor history untouched.
	if _, err = tx.ExecContext(ctx, `DELETE FROM discovery_monitor_links l USING monitors m WHERE l.monitor_id=m.id AND m.deleted_at IS NOT NULL AND l.service_id=$1 AND l.suggestion_key=$2`, service, req.Key); err != nil {
		return uuid.Nil, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO discovery_monitor_links(tenant_id,service_id,monitor_id,suggestion_key) VALUES($1,$2,$3,$4)`, tenant, service, m.ID, req.Key); err != nil {
		return uuid.Nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return uuid.Nil, false, err
	}
	return m.ID, true, nil
}
func (s *Service) Link(ctx context.Context, tenant, service, monitor uuid.UUID, key *string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = s.lockService(ctx, tx, tenant, service); err != nil {
		return err
	}
	var found uuid.UUID
	err = tx.QueryRowContext(ctx, `SELECT id FROM monitors WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL FOR SHARE`, monitor, tenant).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if key != nil {
		var valid bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM discovered_resources r,jsonb_array_elements(r.suggestions) p WHERE r.service_id=$1 AND r.missing_since IS NULL AND p->>'key'=$2)`, service, *key).Scan(&valid)
		if err != nil {
			return err
		}
		if !valid {
			return invalid("suggestion is no longer present")
		}
	}
	if key != nil {
		if _, err = tx.ExecContext(ctx, `DELETE FROM discovery_monitor_links l USING monitors m WHERE l.monitor_id=m.id AND m.deleted_at IS NOT NULL AND l.service_id=$1 AND l.suggestion_key=$2`, service, *key); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO discovery_monitor_links(tenant_id,service_id,monitor_id,suggestion_key) VALUES($1,$2,$3,$4) ON CONFLICT(service_id,monitor_id) DO UPDATE SET suggestion_key=COALESCE(EXCLUDED.suggestion_key,discovery_monitor_links.suggestion_key)`, tenant, service, monitor, key)
	if err != nil {
		if pgerr.IsUniqueViolation(err) {
			return invalid("this suggestion is already linked to another monitor; unlink it first")
		}
		return err
	}
	return tx.Commit()
}
func (s *Service) Unlink(ctx context.Context, tenant, service, monitor uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM discovery_monitor_links WHERE tenant_id=$1 AND service_id=$2 AND monitor_id=$3`, tenant, service, monitor)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
