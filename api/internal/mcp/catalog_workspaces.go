package mcp

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// workspaceAdminTools are the superadmin workspace (tenant) lifecycle tools.
func workspaceAdminTools() []spec {
	workspace := map[string]string{"id": "Workspace (tenant) ID, from list_tenants."}
	name := map[string]string{"name": "Workspace name, unique among live workspaces (1-100 characters)."}
	return []spec{
		{
			Name:        "create_tenant",
			Title:       "Create workspace",
			Description: "Create a workspace (tenant). Superadmins only; they act in every workspace, so no membership is added.",
			Method:      http.MethodPost, Route: "/tenants", Platform: true,
			Body: typeOf[models.CreateTenantRequest](), Required: []string{"name"}, FieldDocs: name,
		},
		{
			Name:        "rename_tenant",
			Title:       "Rename workspace",
			Description: "Rename a workspace. Superadmins only.",
			Method:      http.MethodPatch, Route: "/tenants/{id}", Platform: true,
			PathDocs: workspace,
			Body:     typeOf[models.RenameTenantRequest](), Required: []string{"name"}, FieldDocs: name,
		},
		{
			Name:  "delete_tenant",
			Title: "Delete workspace",
			Description: "Delete a workspace and everything in it: monitors, history, alerts, status pages, channels, " +
				"API keys and memberships. Access stops at once and the data is purged in the background; it " +
				"cannot be restored. confirm_name must repeat the workspace's name. The default workspace and " +
				"the last remaining one cannot be deleted. Superadmins only.",
			Method: http.MethodDelete, Route: "/tenants/{id}", Platform: true,
			PathDocs: workspace, Destructive: true,
			Body: typeOf[models.DeleteTenantRequest](), Required: []string{"confirm_name"},
			FieldDocs: map[string]string{"confirm_name": "The workspace's current name, typed again to confirm."},
		},
	}
}
