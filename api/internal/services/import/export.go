// Export: writing a tenant's monitors in the portable format the importer reads back.

package importservice

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// ExportMonitors exports all tenant monitors as a portable YAML bundle.
func (s *Service) ExportMonitors(ctx context.Context, tenantID uuid.UUID) ([]byte, error) {
	monitors, err := s.loadAllMonitors(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	nameByMonitorID := make(map[uuid.UUID]string, len(monitors))
	for _, monitor := range monitors {
		nameByMonitorID[monitor.ID] = monitor.Name
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

	exported := make([]models.PortableExportMonitor, 0, len(monitors))
	for _, monitor := range monitors {
		configValue, err := decodeRawJSON(monitor.Config)
		if err != nil {
			return nil, fmt.Errorf("failed to decode config for monitor %s: %w", monitor.ID, err)
		}

		groupMembers := make([]string, 0, len(monitor.MemberIDs))
		for _, memberID := range monitor.MemberIDs {
			if name, ok := nameByMonitorID[memberID]; ok {
				groupMembers = append(groupMembers, name)
			}
		}

		exported = append(exported, models.PortableExportMonitor{
			Name:                         monitor.Name,
			Type:                         monitor.Type,
			IntervalSeconds:              monitor.IntervalSeconds,
			TimeoutSeconds:               monitor.TimeoutSeconds,
			Enabled:                      monitor.Enabled,
			Tags:                         monitor.Tags,
			Config:                       configValue,
			GroupMembers:                 groupMembers,
			ConsecutiveFailuresThreshold: monitor.ConsecutiveFailuresThreshold,
		})
	}

	return yaml.Marshal(models.PortableMonitorExport{
		Kind:     "monitor_export",
		Version:  1,
		Monitors: exported,
	})
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
