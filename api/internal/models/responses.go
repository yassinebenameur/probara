package models

// Small response bodies that handlers used to build as map literals. Named,
// so the OpenAPI document reflects exactly what the handler writes.

// DependencyMonitorList is GET /monitors/{id}/dependencies and /dependents.
type DependencyMonitorList struct {
	Items []DependencyMonitor `json:"items"`
}

// BulkAlertingResponse is POST /monitors/bulk/alerting.
type BulkAlertingResponse struct {
	Updated int `json:"updated"`
}

// DeleteLocationResponse is DELETE /locations/{id}.
type DeleteLocationResponse struct {
	MonitorsDetached int `json:"monitors_detached"`
}

// SLAListResponse is GET /slas.
type SLAListResponse struct {
	Items []SLA `json:"items"`
}

// SLAReportRefListResponse is GET /slas/{id}/reports.
type SLAReportRefListResponse struct {
	Items []SLAReportRef `json:"items"`
}

// AuditActionListResponse is GET /audit-log/actions.
type AuditActionListResponse struct {
	Actions []string `json:"actions"`
}

// MeshEdgeHistoryResponse is GET /mesh/history.
type MeshEdgeHistoryResponse struct {
	Points []MeshEdgeHistoryPoint `json:"points"`
}

// PushAcceptedResponse answers an accepted push heartbeat.
type PushAcceptedResponse struct {
	Status string `json:"status"`
}
