package statuspage

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/google/uuid"
)

type monitorPresenter interface {
	Configure(monitor *MonitorStatus, configJSON []byte)
	ResolveOperationalIDs(ctx context.Context, svc *Service, monitorID, tenantID uuid.UUID) ([]uuid.UUID, error)
}

type regularMonitorPresenter struct {
	urlExtractor func(configJSON []byte) string
}

func (p regularMonitorPresenter) Configure(monitor *MonitorStatus, configJSON []byte) {
	monitor.URL = ""
	if p.urlExtractor != nil {
		monitor.URL = p.urlExtractor(configJSON)
	}
}

func (p regularMonitorPresenter) ResolveOperationalIDs(_ context.Context, _ *Service, monitorID, _ uuid.UUID) ([]uuid.UUID, error) {
	return []uuid.UUID{monitorID}, nil
}

type groupMonitorPresenter struct{}

func (p groupMonitorPresenter) Configure(monitor *MonitorStatus, _ []byte) {
	monitor.URL = "Group Monitor"
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
		"tcp": regularMonitorPresenter{
			urlExtractor: extractHostPortTarget,
		},
		"agent": regularMonitorPresenter{
			urlExtractor: func(_ []byte) string { return "System Agent" },
		},
		"push": regularMonitorPresenter{
			urlExtractor: func(_ []byte) string { return "Push Monitor" },
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

// extractSIPTarget is a SIP monitor's public target: its signalling server
// (host, with the port the worker dials). An INVITE test call deliberately
// shows that host too, never call.number or call.target_uri: monitor.URL is
// public on every page whatever show_monitor_url says (the detail line, the
// data-search text, a custom template's .URL), so a dialled number would leak
// to every visitor. This departs from the design's "number@domain" on
// purpose; the operator UI's monitorTarget, which only signed-in users see,
// differs.
func extractSIPTarget(configJSON []byte) string {
	var config map[string]interface{}
	if err := json.Unmarshal(configJSON, &config); err != nil {
		return ""
	}

	host, _ := config["host"].(string)
	if strings.TrimSpace(host) == "" {
		return ""
	}

	// Under RFC 3263 without a port, SRV picks the servers and their
	// ports: the target is the domain itself. An IP host skips SRV, so it
	// falls through to the default port the worker dials.
	port, _ := config["port"].(float64)
	isIP := net.ParseIP(strings.Trim(strings.TrimSpace(host), "[]")) != nil
	if resolution, _ := config["resolution"].(string); port == 0 && !isIP && strings.EqualFold(resolution, "rfc3263") {
		return host
	}

	// Same defaults the worker dials: 5061 over TLS, 5060 otherwise.
	if port == 0 {
		port = 5060
		if transport, _ := config["transport"].(string); strings.EqualFold(transport, "tls") {
			port = 5061
		}
	}

	return fmt.Sprintf("%s:%d", host, int(port))
}

// extractHostPortTarget renders a "host:port" target for raw connect checks
// (TCP), falling back to just the host when no port is configured.
func extractHostPortTarget(configJSON []byte) string {
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
		return host
	}

	return fmt.Sprintf("%s:%d", host, int(port))
}
