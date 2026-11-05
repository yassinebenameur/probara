package mcp

import "net/http"

// deliveryTools reads the notification delivery history: each attempt to
// deliver an alert event to a channel, kept 90 days.
func deliveryTools() []spec {
	const outcomes = "Each row has event_type, attempt, path (sync: the alerter sent it; async: queued for, " +
		"then sent by, a delivery worker), outcome (queued, delivered, failed = transient and retried, " +
		"permanent_failure = rejected and not retried, dropped_superseded = the alert resolved first, " +
		"dropped_ack = held by an ack or snooze and sent once it lifts) and a sanitized error."
	return []spec{
		{
			Name:        "list_alert_deliveries",
			Title:       "List alert deliveries",
			Description: "Every notification attempt for one alert across its channels, newest first. " + outcomes,
			Method:      http.MethodGet, Route: "/alerts/{id}/deliveries",
			PathDocs: map[string]string{"id": "Alert ID."},
			Query:    withPaging(),
		},
		{
			Name:  "list_channel_deliveries",
			Title: "List alert channel deliveries",
			Description: "Every notification attempt on one alert channel, newest first, with the alert's kind and " +
				"monitor name. Use it to see why a channel stopped delivering. " + outcomes,
			Method: http.MethodGet, Route: "/alert-channels/{id}/deliveries",
			PathDocs: map[string]string{"id": "Alert channel ID."},
			Query:    withPaging(),
		},
	}
}
