package alerter

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/autoincident"
	"github.com/yassinebenameur/probara/shared/incidentrecovery"
)

func (a *Alerter) ensureAutoIncidentForAlertTx(ctx context.Context, tx *sql.Tx, binding alertSubject, alert *alertRecord) error {
	if !binding.CreateIncidentOnFire {
		return nil
	}

	if err := a.lockAutoIncidentKeyTx(ctx, tx, binding.TenantID, binding.MonitorID); err != nil {
		return err
	}

	incidentID, created, err := a.findOrCreateAutoIncidentTx(ctx, tx, binding)
	if err != nil {
		return err
	}

	alertAttached, err := a.ensureIncidentAlertLinkTx(ctx, tx, incidentID, alert.ID)
	if err != nil {
		return err
	}
	monitorAttached, err := a.ensureIncidentMonitorLinkTx(ctx, tx, incidentID, binding.MonitorID)
	if err != nil {
		return err
	}

	if created || alertAttached || monitorAttached {
		message := "Alert auto-linked to existing incident"
		if created {
			message = "Incident auto-created from firing alert"
		}
		metadata := map[string]interface{}{
			"alert_id":   alert.ID.String(),
			"monitor_id": binding.MonitorID.String(),
		}
		if err := incidentrecovery.InsertTimelineEntryTx(ctx, tx, binding.TenantID, incidentID, incidentrecovery.SystemEntryType, message, metadata); err != nil {
			return err
		}
	}

	return nil
}

// findOrCreateAutoIncidentTx returns the monitor's open automatic incident,
// creating it when there is none (shared/autoincident, which the API's
// status-page auto-publish uses too, so both land on the same incident).
func (a *Alerter) findOrCreateAutoIncidentTx(ctx context.Context, tx *sql.Tx, binding alertSubject) (uuid.UUID, bool, error) {
	title, summary := buildAlerterAutoIncidentNarrative(binding.MonitorName)
	return autoincident.FindOrCreateTx(ctx, tx, binding.TenantID, binding.MonitorID, title, summary)
}

// autoIncidentLockKey is autoincident.LockKey, the key that serializes
// automatic incident creation for one monitor across services and releases.
func autoIncidentLockKey(tenantID, monitorID uuid.UUID) string {
	return autoincident.LockKey(tenantID, monitorID)
}

func (a *Alerter) lockAutoIncidentKeyTx(ctx context.Context, tx *sql.Tx, tenantID, monitorID uuid.UUID) error {
	return autoincident.LockTx(ctx, tx, tenantID, monitorID)
}

func (a *Alerter) ensureIncidentAlertLinkTx(ctx context.Context, tx *sql.Tx, incidentID, alertID uuid.UUID) (bool, error) {
	return autoincident.LinkAlertTx(ctx, tx, incidentID, alertID)
}

func (a *Alerter) ensureIncidentMonitorLinkTx(ctx context.Context, tx *sql.Tx, incidentID, monitorID uuid.UUID) (bool, error) {
	return autoincident.LinkMonitorTx(ctx, tx, incidentID, monitorID)
}

func buildAlerterAutoIncidentNarrative(monitorName string) (string, string) {
	monitorName = strings.TrimSpace(monitorName)

	title := "Auto-created incident"
	if monitorName != "" {
		title = fmt.Sprintf("%s incident", monitorName)
	}

	return title, "Automatically created from a firing alert."
}
