package mcp

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/models"
)

var applyFieldDocs = map[string]string{
	"document": "The monitors document: the YAML (or JSON) text of export_monitors with keyed=true, edited. " +
		"kind monitor_export, version 2, every monitor with a key. Secrets read \"***\" keep the stored value; " +
		"a literal value replaces it, \"\" clears it.",
	"prune":       "Also delete the workspace's keyed monitors the document leaves out (never a monitor without a key).",
	"prune_scope": "Key prefix (\"payments.\"): prune only deletes keyed monitors whose key starts with it, and every key of the document must. Use one per team sharing a workspace.",
	"force":       "Let prune through its guard (an empty document, or more than half of the keyed monitors in scope deleted), and let a probable key rename (a create and a delete with the same name and type) replace the monitor.",
	"fingerprint": "fingerprint from plan_monitor_apply: refused if the document or the keyed monitors changed since.",
}

// monitorApplyTools are config-as-code: plan and apply a monitors document.
func monitorApplyTools() []spec {
	return []spec{
		{
			Name:  "plan_monitor_apply",
			Title: "Plan monitor apply",
			Description: "Compute, without writing anything, what apply_monitors would do with a monitors document: " +
				"per key create, update (field diff; secrets only as changed, never values), unchanged, or delete " +
				"(keyed monitors the document leaves out), validation errors, the prune guard and a fingerprint.",
			Method: http.MethodPost, Route: "/monitors/apply/plan",
			Body: typeOf[models.MonitorApplyRequest](), Required: []string{"document"}, FieldDocs: applyFieldDocs,
		},
		{
			Name:  "apply_monitors",
			Title: "Apply monitors",
			Description: "Bring the workspace's keyed monitors in line with a monitors document, all or nothing; " +
				"applying it again changes nothing. Run plan_monitor_apply first and pass its fingerprint. Deletes " +
				"only with prune=true, and only keyed monitors.",
			Method: http.MethodPost, Route: "/monitors/apply",
			Body: typeOf[models.MonitorApplyRequest](), Required: []string{"document"}, FieldDocs: applyFieldDocs,
			Destructive: true,
		},
	}
}
