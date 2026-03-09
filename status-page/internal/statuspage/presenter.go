package statuspage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type monitorPresenter interface {
	Configure(monitor *MonitorStatus, configJSON []byte)
	Populate(ctx context.Context, svc *Service, monitor *MonitorStatus, monitorID, tenantID uuid.UUID)
	ResolveOperationalIDs(ctx context.Context, svc *Service, monitorID, tenantID uuid.UUID) ([]uuid.UUID, error)
}

type regularMonitorPresenter struct {
	urlExtractor func(configJSON []byte) string
	afterLoad    func(ctx context.Context, svc *Service, monitor *MonitorStatus, monitorID, tenantID uuid.UUID)
}

func (p regularMonitorPresenter) Configure(monitor *MonitorStatus, configJSON []byte) {
	monitor.URL = ""
	if p.urlExtractor != nil {
		monitor.URL = p.urlExtractor(configJSON)
	}
}

func (p regularMonitorPresenter) Populate(ctx context.Context, svc *Service, monitor *MonitorStatus, monitorID, tenantID uuid.UUID) {
	svc.populateRegularMonitorStatus(ctx, monitor, monitorID, tenantID)
	if p.afterLoad != nil {
		p.afterLoad(ctx, svc, monitor, monitorID, tenantID)
	}
}

func (p regularMonitorPresenter) ResolveOperationalIDs(_ context.Context, _ *Service, monitorID, _ uuid.UUID) ([]uuid.UUID, error) {
	return []uuid.UUID{monitorID}, nil
}

type groupMonitorPresenter struct{}

func (p groupMonitorPresenter) Configure(monitor *MonitorStatus, _ []byte) {
	monitor.URL = "Group Monitor"
}

func (p groupMonitorPresenter) Populate(ctx context.Context, svc *Service, monitor *MonitorStatus, monitorID, tenantID uuid.UUID) {
	svc.populateGroupMonitorStatus(ctx, monitor, monitorID, tenantID)
}

func (p groupMonitorPresenter) ResolveOperationalIDs(ctx context.Context, svc *Service, monitorID, tenantID uuid.UUID) ([]uuid.UUID, error) {
	return svc.getGroupMemberIDs(ctx, monitorID, tenantID)
}

func newMonitorPresenters() map[string]monitorPresenter {
	return map[string]monitorPresenter{
		"http": regularMonitorPresenter{
			urlExtractor: func(configJSON []byte) string { return extractConfigString(configJSON, "url") },
		},
		"ping": regularMonitorPresenter{
			urlExtractor: func(configJSON []byte) string { return extractConfigString(configJSON, "host") },
		},
		"dns": regularMonitorPresenter{
			urlExtractor: func(configJSON []byte) string { return extractConfigString(configJSON, "host") },
		},
		"sip": regularMonitorPresenter{
			urlExtractor: extractSIPTarget,
		},
		"agent": regularMonitorPresenter{
			urlExtractor: func(_ []byte) string { return "System Agent" },
			afterLoad: func(ctx context.Context, svc *Service, monitor *MonitorStatus, monitorID, tenantID uuid.UUID) {
				agentMetrics, err := svc.GetLatestAgentMetrics(ctx, monitorID, tenantID)
				if err == nil {
					monitor.AgentMetrics = agentMetrics
				}
			},
		},
		"push": regularMonitorPresenter{
			urlExtractor: func(_ []byte) string { return "Push Monitor" },
			afterLoad: func(ctx context.Context, svc *Service, monitor *MonitorStatus, monitorID, tenantID uuid.UUID) {
				pushMetrics, err := svc.GetLatestPushMetrics(ctx, monitorID, tenantID)
				if err == nil {
					monitor.PushMetrics = pushMetrics
				}
			},
		},
		"group": groupMonitorPresenter{},
	}
}

func extractConfigString(configJSON []byte, key string) string {
	var config map[string]interface{}
	if err := json.Unmarshal(configJSON, &config); err != nil {
		return ""
	}
	value, _ := config[key].(string)
	return value
}

func extractSIPTarget(configJSON []byte) string {
	var config map[string]interface{}
	if err := json.Unmarshal(configJSON, &config); err != nil {
		return ""
	}

	host, _ := config["host"].(string)
	if strings.TrimSpace(host) == "" {
		return ""
	}

	port, _ := config["port"].(float64)
	if port == 0 {
		port = 5060
	}

	return fmt.Sprintf("%s:%d", host, int(port))
}
