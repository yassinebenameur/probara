// Alerter service configuration.

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// AlerterConfig contains configuration for the alerter service
type AlerterConfig struct {
	BaseConfig
	SMTPConfig
	NotificationEgressConfig
	AlertStream                  string
	AlertSubject                 string
	AlertEvalIntervalSeconds     int
	AlertReminderIntervalSeconds int
	AlertGroupWindowSeconds      int
	AlertGroupMaxChildren        int
	LatencyAnomalyEnabled        bool
	AlertEmailTo                 string

	// AsyncDispatch toggles publishing channel notifications to the NATS
	// NOTIFICATIONS stream instead of dispatching synchronously inside the
	// alerter loop. When true, the worker consumes "alerts.dispatch.*" and
	// invokes the plugin Send path.
	AsyncDispatch            bool
	NotificationsStream      string
	NotificationsSubjectGlob string

	// SuspectedCausesDisabled turns off the opt-in suspected-cause follow-ups
	// (alerter dispatchSuspectedCauses) for the whole installation. It is set
	// by the local AI kill switches, LOCAL_AI_DISABLED=true or
	// AI_TRIAGE_ENABLED=false, so disabling AI also stops follow-ups from
	// triages that were already ready. The zero value sends them.
	SuspectedCausesDisabled bool
}

// LoadAlerterConfig loads alerter service configuration
func LoadAlerterConfig() (*AlerterConfig, error) {
	base, err := LoadBaseConfig("alerter")
	if err != nil {
		return nil, err
	}

	cfg := &AlerterConfig{BaseConfig: *base}

	// ALERT_STREAM
	alertStream := os.Getenv("ALERT_STREAM")
	if alertStream == "" {
		cfg.AlertStream = "ALERTS"
	} else {
		cfg.AlertStream = alertStream
	}

	// ALERT_SUBJECT
	alertSubject := os.Getenv("ALERT_SUBJECT")
	if alertSubject == "" {
		cfg.AlertSubject = "alerts"
	} else {
		cfg.AlertSubject = alertSubject
	}

	// ALERT_EVAL_INTERVAL_SECONDS
	evalIntervalStr := os.Getenv("ALERT_EVAL_INTERVAL_SECONDS")
	if evalIntervalStr == "" {
		cfg.AlertEvalIntervalSeconds = 30
	} else {
		val, err := strconv.Atoi(evalIntervalStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ALERT_EVAL_INTERVAL_SECONDS: %w", err)
		}
		cfg.AlertEvalIntervalSeconds = val
	}

	// ALERTER_LATENCY_ANOMALY_ENABLED (default true): kill-switch for the
	// latency anomaly detection step.
	cfg.LatencyAnomalyEnabled = true
	if v := os.Getenv("ALERTER_LATENCY_ANOMALY_ENABLED"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid ALERTER_LATENCY_ANOMALY_ENABLED: %w", err)
		}
		cfg.LatencyAnomalyEnabled = enabled
	}

	// ALERT_REMINDER_INTERVAL_SECONDS
	reminderIntervalStr := os.Getenv("ALERT_REMINDER_INTERVAL_SECONDS")
	if reminderIntervalStr == "" {
		cfg.AlertReminderIntervalSeconds = 3600
	} else {
		val, err := strconv.Atoi(reminderIntervalStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ALERT_REMINDER_INTERVAL_SECONDS: %w", err)
		}
		cfg.AlertReminderIntervalSeconds = val
	}

	// ALERT_GROUP_WINDOW_SECONDS
	groupWindowStr := os.Getenv("ALERT_GROUP_WINDOW_SECONDS")
	if groupWindowStr == "" {
		cfg.AlertGroupWindowSeconds = 60
	} else {
		val, err := strconv.Atoi(groupWindowStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ALERT_GROUP_WINDOW_SECONDS: %w", err)
		}
		cfg.AlertGroupWindowSeconds = val
	}

	// ALERT_GROUP_MAX_CHILDREN
	groupMaxChildrenStr := os.Getenv("ALERT_GROUP_MAX_CHILDREN")
	if groupMaxChildrenStr == "" {
		cfg.AlertGroupMaxChildren = 5
	} else {
		val, err := strconv.Atoi(groupMaxChildrenStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ALERT_GROUP_MAX_CHILDREN: %w", err)
		}
		cfg.AlertGroupMaxChildren = val
	}

	// SMTP_* transport
	smtpCfg, err := loadSMTPConfig()
	if err != nil {
		return nil, err
	}
	cfg.SMTPConfig = smtpCfg
	egressCfg, err := loadNotificationEgressConfig()
	if err != nil {
		return nil, err
	}
	cfg.NotificationEgressConfig = egressCfg

	// ALERT_EMAIL_TO
	cfg.AlertEmailTo = os.Getenv("ALERT_EMAIL_TO")

	// ALERTER_ASYNC_DISPATCH — when true, dispatchNotifications publishes to
	// NATS rather than calling plugin.Send inline. Defaults to false so
	// rollout is opt-in per environment.
	asyncStr := strings.TrimSpace(os.Getenv("ALERTER_ASYNC_DISPATCH"))
	if asyncStr != "" {
		async, err := strconv.ParseBool(asyncStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ALERTER_ASYNC_DISPATCH: %w", err)
		}
		cfg.AsyncDispatch = async
	}

	cfg.NotificationsStream = envOrDefault("NOTIFICATIONS_STREAM", "NOTIFICATIONS")
	cfg.NotificationsSubjectGlob = envOrDefault("NOTIFICATIONS_SUBJECT_GLOB", "alerts.dispatch.>")

	// The alerter never calls local AI; it reads only the two kill switches.
	localAIDisabled, err := loadLocalAIDisabled()
	if err != nil {
		return nil, err
	}
	triageEnabled, err := loadAITriageEnabled()
	if err != nil {
		return nil, err
	}
	cfg.SuspectedCausesDisabled = localAIDisabled || !triageEnabled

	return cfg, nil
}
