package middleware

import (
	"net/http"
	"testing"
)

func TestDeriveAction(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   string
	}{
		{http.MethodPost, "/api/v1/monitors", "monitor.create"},
		{http.MethodPatch, "/api/v1/monitors/3f2a6c1e-0000-0000-0000-000000000001", "monitor.update"},
		{http.MethodDelete, "/api/v1/monitors/3f2a6c1e-0000-0000-0000-000000000001", "monitor.delete"},
		{http.MethodPost, "/api/v1/incidents/3f2a6c1e-0000-0000-0000-000000000001/state", "incident.state"},
		{http.MethodPost, "/api/v1/monitors/bulk/delete", "monitor.delete"},
		{http.MethodPost, "/api/v1/alerts/3f2a6c1e-0000-0000-0000-000000000001/acknowledge", "alert.acknowledge"},
		{http.MethodPut, "/api/v1/notification-settings", "notification-setting.update"},
		{http.MethodPatch, "/api/v1/tenant-settings", "tenant-setting.update"},
		{http.MethodPost, "/api/v1/status-pages", "status-page.create"},
	}

	for _, tc := range cases {
		if got := deriveAction(tc.method, tc.path); got != tc.want {
			t.Errorf("deriveAction(%s %s) = %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestDeriveResource(t *testing.T) {
	resourceType, resourceID := deriveResource("/api/v1/monitors/3f2a6c1e-0000-0000-0000-000000000001/dependencies/3f2a6c1e-0000-0000-0000-000000000002")
	if resourceType != "monitor" {
		t.Errorf("resourceType = %q, want monitor", resourceType)
	}
	if resourceID != "3f2a6c1e-0000-0000-0000-000000000001" {
		t.Errorf("resourceID = %q, want first UUID", resourceID)
	}

	resourceType, resourceID = deriveResource("/api/v1/monitors")
	if resourceType != "monitor" || resourceID != "" {
		t.Errorf("collection route: got (%q, %q)", resourceType, resourceID)
	}
}

func TestOutcomeFromStatus(t *testing.T) {
	cases := map[int]string{
		200: "success",
		201: "success",
		204: "success",
		400: "failure",
		401: "denied",
		403: "denied",
		404: "failure",
		500: "failure",
	}
	for status, want := range cases {
		if got := outcomeFromStatus(status); got != want {
			t.Errorf("outcomeFromStatus(%d) = %q, want %q", status, got, want)
		}
	}
}
