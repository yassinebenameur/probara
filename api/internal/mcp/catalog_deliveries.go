package mcp

import "net/http"

// deliveryTools read the notification delivery history: every attempt to
// deliver an alert notification, kept 90 days.
func deliveryTools() []spec {
	const rows = "Each row has event_type (created, reminder, resolved, acknowledged, suspected_cause), " +
		"attempt, path (sync: sent by the alerter; async: queued, then sent by a delivery worker), " +
		"test (a channel test send), recipient (masked; one row per recipient on SMS), outcome and reason, " +
		"a sanitized error, provider_status and duration_ms. Outcomes: delivered; failed (transient, retried; " +
		"reason retries_exhausted on the worker's last attempt, plugin_missing when the worker build lacks " +
		"the channel type); permanent_failure (not retried; reason egress_blocked when the notification " +
		"egress policy refused the address, invalid_config); dropped (a queued event deliberately not sent: " +
		"reason resolved = the alert resolved first, unacknowledged = an acknowledgement notice after an " +
		"unacknowledge, muted = held by an acknowledgement or snooze, channel_inactive, channel_changed). " +
		"Drops are not failures."
	return []spec{
		{
			Name:        "list_alert_deliveries",
			Title:       "List alert deliveries",
			Description: "Every notification attempt for one alert across its channels, oldest first. " + rows,
			Method:      http.MethodGet, Route: "/alerts/{id}/deliveries",
			PathDocs: map[string]string{"id": "Alert ID."},
			Query: []param{
				{Name: "page", Type: "integer", Description: "1-based page number (default 1)."},
				{Name: "page_size", Type: "integer", Description: "Items per page, 1-200 (default 50)."},
			},
		},
		{
			Name:  "list_channel_deliveries",
			Title: "List alert channel deliveries",
			Description: "One alert channel's notification attempts, test sends included, newest first, with each " +
				"alert's kind and monitor name. Use it to see why a channel stopped delivering; get_alert_channel " +
				"carries the failing flag (a failure in the last 24 hours with no success since). " + rows,
			Method: http.MethodGet, Route: "/alert-channels/{id}/deliveries",
			PathDocs: map[string]string{"id": "Alert channel ID."},
			Query: []param{
				{Name: "outcome", Description: "Only these outcomes, comma-separated: delivered, failed, permanent_failure, dropped."},
				{Name: "page", Type: "integer", Description: "1-based page number (default 1)."},
				{Name: "page_size", Type: "integer", Description: "Items per page, 1-200 (default 50)."},
			},
		},
	}
}
