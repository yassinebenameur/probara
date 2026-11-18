// Export: writing a tenant's monitors in the portable format the importer reads back.

package importservice

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/yassinebenameur/probara/api/internal/dbtx"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// ExportOptions narrows an export.
type ExportOptions struct {
	// KeyedOnly exports only the monitors with a key: the document apply
	// manages (POST /monitors/apply).
	KeyedOnly bool
}

// serverOwnedConfigFields are config fields the server writes, which a
// document neither carries nor changes: a group's monitor_ids (membership is
// group_members / member_keys, and the junction table), an agent's
// agent_id. The export leaves them out and apply keeps the stored values.
// (A push monitor's push_token is a registry secret: masked, and kept.)
var serverOwnedConfigFields = map[models.MonitorType][]string{
	models.MonitorTypeGroup: {"monitor_ids"},
	models.MonitorTypeAgent: {"agent_id"},
}

// ExportMonitors exports the tenant's monitors as a portable YAML document.
// Version 2 adds each monitor's key, and its keyed group members and
// dependencies by key, to what version 1 carried; an export of all monitors
// in which none has a key is version 1. A keyed export (the file apply
// reads) names group members by key only: apply refuses a group_members
// list that disagrees with member_keys, and the importer resolves
// member_keys itself. Secrets are masked.
func (s *Service) ExportMonitors(ctx context.Context, tenantID uuid.UUID, opts ExportOptions) ([]byte, error) {
	monitors, err := s.loadAllMonitors(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	dependsOn, err := s.loadDependencies(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	sort.SliceStable(monitors, func(i, j int) bool {
		if monitors[i].Type == models.MonitorTypeGroup && monitors[j].Type != models.MonitorTypeGroup {
			return false
		}
		if monitors[i].Type != models.MonitorTypeGroup && monitors[j].Type == models.MonitorTypeGroup {
			return true
		}
		return monitors[i].CreatedAt.Before(monitors[j].CreatedAt)
	})

	// After the sort, which moves the elements these point at.
	byID := make(map[uuid.UUID]*models.Monitor, len(monitors))
	for i := range monitors {
		byID[monitors[i].ID] = &monitors[i]
	}
	included := func(m *models.Monitor) bool { return !opts.KeyedOnly || m.Key != nil }

	exported := make([]models.PortableExportMonitor, 0, len(monitors))
	for _, monitor := range monitors {
		if !included(&monitor) {
			continue
		}
		// Masked as a version snapshot is (secrets.MaskStoredMonitorConfig):
		// registry secrets, credentials in plain headers or variables, and
		// the credentials of URLs (a user:password@ part, a credential-named
		// query parameter), since the file is meant to be committed.
		config := monitor.Config
		if masked, err := secrets.MaskStoredMonitorConfig(string(monitor.Type), config); err == nil {
			config = masked
		}
		configValue, err := decodeRawJSON(config)
		if err != nil {
			return nil, fmt.Errorf("failed to decode config for monitor %s: %w", monitor.ID, err)
		}
		if configMap, ok := configValue.(map[string]interface{}); ok {
			for _, field := range serverOwnedConfigFields[monitor.Type] {
				delete(configMap, field)
			}
		}

		// Members and dependencies outside the export are left out: a file
		// only names monitors it holds.
		groupMembers := make([]string, 0, len(monitor.MemberIDs))
		var memberKeys []string
		for _, memberID := range monitor.MemberIDs {
			member, ok := byID[memberID]
			if !ok || !included(member) {
				continue
			}
			if !opts.KeyedOnly {
				groupMembers = append(groupMembers, member.Name)
			}
			if member.Key != nil {
				memberKeys = append(memberKeys, *member.Key)
			}
		}
		var dependencyKeys []string
		for _, upstreamID := range dependsOn[monitor.ID] {
			if upstream, ok := byID[upstreamID]; ok && upstream.Key != nil {
				dependencyKeys = append(dependencyKeys, *upstream.Key)
			}
		}
		sort.Strings(memberKeys)
		sort.Strings(dependencyKeys)

		entry := models.PortableExportMonitor{
			Name:                         monitor.Name,
			Type:                         monitor.Type,
			IntervalSeconds:              monitor.IntervalSeconds,
			TimeoutSeconds:               monitor.TimeoutSeconds,
			Enabled:                      &monitor.Enabled,
			Tags:                         monitor.Tags,
			Config:                       configValue,
			GroupMembers:                 groupMembers,
			MemberKeys:                   memberKeys,
			DependsOn:                    dependencyKeys,
			ConsecutiveFailuresThreshold: monitor.ConsecutiveFailuresThreshold,
			RecoveryThreshold:            monitor.RecoveryThreshold,
		}
		if monitor.Key != nil {
			entry.Key = *monitor.Key
		}
		exported = append(exported, entry)
	}

	// A plain copy with no key in it stays version 1, which older releases
	// import; version 2 only when it carries keys (or was asked for them).
	version := models.PortableExportVersion1
	for _, entry := range exported {
		if entry.Key != "" {
			version = models.PortableExportVersion2
		}
	}
	if opts.KeyedOnly {
		version = models.PortableExportVersion2
	}
	return yaml.Marshal(models.PortableMonitorExport{
		Kind:     models.PortableMonitorExportKind,
		Version:  version,
		Monitors: exported,
	})
}

// loadDependencies reads every dependency edge between the tenant's live
// monitors, by dependent monitor, in one query.
func (s *Service) loadDependencies(ctx context.Context, tenantID uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	out := map[uuid.UUID][]uuid.UUID{}
	if s.db == nil {
		return out, nil
	}
	rows, err := dbtx.Of(ctx, s.db).QueryContext(ctx, `
		SELECT md.monitor_id, md.depends_on_id
		FROM monitor_dependencies md
		JOIN monitors m ON m.id = md.monitor_id AND m.tenant_id = $1 AND m.deleted_at IS NULL
		JOIN monitors u ON u.id = md.depends_on_id AND u.tenant_id = $1 AND u.deleted_at IS NULL
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to load dependencies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var monitorID, upstreamID uuid.UUID
		if err := rows.Scan(&monitorID, &upstreamID); err != nil {
			return nil, fmt.Errorf("failed to scan dependency: %w", err)
		}
		out[monitorID] = append(out[monitorID], upstreamID)
	}
	return out, rows.Err()
}

func (s *Service) loadAllMonitors(ctx context.Context, tenantID uuid.UUID) ([]models.Monitor, error) {
	const pageSize = 100

	all := make([]models.Monitor, 0)
	for page := 1; ; page++ {
		resp, err := s.monitorService.ListMonitors(ctx, tenantID, nil, nil, page, pageSize)
		if err != nil {
			return nil, fmt.Errorf("failed to list monitors: %w", err)
		}
		all = append(all, resp.Items...)
		if len(all) >= resp.Total || len(resp.Items) == 0 {
			break
		}
	}

	for i := range all {
		if all[i].Type != models.MonitorTypeGroup {
			continue
		}
		monitor, err := s.monitorService.GetMonitor(ctx, tenantID, all[i].ID)
		if err != nil {
			return nil, fmt.Errorf("failed to load group members for monitor %s: %w", all[i].ID, err)
		}
		all[i].MemberIDs = monitor.MemberIDs
	}

	return all, nil
}

func decodeRawJSON(raw json.RawMessage) (interface{}, error) {
	if len(raw) == 0 {
		return map[string]interface{}{}, nil
	}
	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}
