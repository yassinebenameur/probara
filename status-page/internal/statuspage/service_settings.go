// The read-side mirror of status page settings. The API owns the write path
// (api/internal/services/statuspages); the two must change together.

package statuspage

import (
	"encoding/json"
	"strings"
)

type statusPageSettingsPatch struct {
	ShowMonitorTags   *bool   `json:"show_monitor_tags,omitempty"`
	ShowMonitorURL    *bool   `json:"show_monitor_url,omitempty"`
	ShowMonitorUptime *bool   `json:"show_monitor_uptime,omitempty"`
	ShowMonitorTLS    *bool   `json:"show_monitor_tls,omitempty"`
	ShowLatencyCharts *bool   `json:"show_latency_charts,omitempty"`
	ShowAgentMetrics  *bool   `json:"show_agent_metrics,omitempty"`
	ShowGlobalUptime  *bool   `json:"show_global_uptime,omitempty"`
	ShowFooter        *bool   `json:"show_footer,omitempty"`
	FooterText        *string `json:"footer_text,omitempty"`
	DefaultTheme      *string `json:"default_theme,omitempty"`
	AllowThemeToggle  *bool   `json:"allow_theme_toggle,omitempty"`

	EnablePushNotifications *bool `json:"enable_push_notifications,omitempty"`

	CustomCSS        *string `json:"custom_css,omitempty"`
	CustomHeadHTML   *string `json:"custom_head_html,omitempty"`
	CustomFooterHTML *string `json:"custom_footer_html,omitempty"`
}

type statusPageSettingsStored struct {
	ShowMonitorTags   bool
	ShowMonitorURL    bool
	ShowMonitorUptime bool
	ShowMonitorTLS    bool
	ShowLatencyCharts bool
	ShowAgentMetrics  bool
	ShowGlobalUptime  bool
	ShowFooter        bool
	FooterText        *string
	DefaultTheme      string
	AllowThemeToggle  bool

	EnablePushNotifications bool

	CustomCSS        string
	CustomHeadHTML   string
	CustomFooterHTML string
}

func defaultStatusPageSettings() statusPageSettingsStored {
	return statusPageSettingsStored{
		ShowMonitorTags:   true,
		ShowMonitorURL:    true,
		ShowMonitorUptime: true,
		ShowMonitorTLS:    true,
		ShowLatencyCharts: true,
		ShowAgentMetrics:  true,
		ShowGlobalUptime:  true,
		ShowFooter:        true,
		DefaultTheme:      "dark",
		AllowThemeToggle:  true,
		// Off by default: enabling it makes the page ask visitors for
		// notification permission, which is the operator's call to make.
		EnablePushNotifications: false,
	}
}

func parseStatusPageSettings(settingsJSON []byte) statusPageSettingsStored {
	stored := defaultStatusPageSettings()
	if len(settingsJSON) == 0 {
		return stored
	}
	var patch statusPageSettingsPatch
	if err := json.Unmarshal(settingsJSON, &patch); err != nil {
		return stored
	}
	if patch.ShowMonitorTags != nil {
		stored.ShowMonitorTags = *patch.ShowMonitorTags
	}
	if patch.ShowMonitorURL != nil {
		stored.ShowMonitorURL = *patch.ShowMonitorURL
	}
	if patch.ShowMonitorUptime != nil {
		stored.ShowMonitorUptime = *patch.ShowMonitorUptime
	}
	if patch.ShowMonitorTLS != nil {
		stored.ShowMonitorTLS = *patch.ShowMonitorTLS
	}
	if patch.ShowLatencyCharts != nil {
		stored.ShowLatencyCharts = *patch.ShowLatencyCharts
	}
	if patch.ShowAgentMetrics != nil {
		stored.ShowAgentMetrics = *patch.ShowAgentMetrics
	}
	if patch.ShowGlobalUptime != nil {
		stored.ShowGlobalUptime = *patch.ShowGlobalUptime
	}
	if patch.ShowFooter != nil {
		stored.ShowFooter = *patch.ShowFooter
	}
	if patch.FooterText != nil {
		v := strings.TrimSpace(*patch.FooterText)
		if v == "" {
			stored.FooterText = nil
		} else {
			stored.FooterText = &v
		}
	}
	if patch.DefaultTheme != nil {
		switch strings.ToLower(strings.TrimSpace(*patch.DefaultTheme)) {
		case "light":
			stored.DefaultTheme = "light"
		default:
			stored.DefaultTheme = "dark"
		}
	}
	if patch.AllowThemeToggle != nil {
		stored.AllowThemeToggle = *patch.AllowThemeToggle
	}
	if patch.EnablePushNotifications != nil {
		stored.EnablePushNotifications = *patch.EnablePushNotifications
	}
	if patch.CustomCSS != nil {
		stored.CustomCSS = strings.TrimSpace(*patch.CustomCSS)
	}
	if patch.CustomHeadHTML != nil {
		stored.CustomHeadHTML = strings.TrimSpace(*patch.CustomHeadHTML)
	}
	if patch.CustomFooterHTML != nil {
		stored.CustomFooterHTML = strings.TrimSpace(*patch.CustomFooterHTML)
	}
	return stored
}
