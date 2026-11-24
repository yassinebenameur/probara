package mcp

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// workspaceAdminTools are the superadmin workspace (tenant) lifecycle tools.
func workspaceAdminTools() []spec {
	workspace := map[string]string{"id": "Workspace (tenant) ID, from list_tenants or list_workspace_overview."}
	name := map[string]string{"name": "Workspace name, unique among live workspaces (1-100 characters, no control characters)."}
	return []spec{
		{
			Name:        "list_workspace_overview",
			Title:       "List workspaces with counts",
			Description: "Every live workspace with its member and monitor counts, and whether it can be deleted. Superadmins only.",
			Method:      http.MethodGet, Route: "/tenants/overview", Platform: true,
		},
		{
			Name:  "create_tenant",
			Title: "Create workspace",
			Description: "Create an empty workspace (tenant): no channels, the shared worker fleet, default notification " +
				"settings. Superadmins only; they act in every workspace, so no membership is added.",
			Method: http.MethodPost, Route: "/tenants", Platform: true,
			Body: typeOf[models.CreateWorkspaceRequest](), Required: []string{"name"}, FieldDocs: name,
		},
		{
			Name:        "rename_tenant",
			Title:       "Rename workspace",
			Description: "Rename a workspace. Superadmins only.",
			Method:      http.MethodPatch, Route: "/tenants/{id}", Platform: true,
			PathDocs: workspace,
			Body:     typeOf[models.RenameWorkspaceRequest](), Required: []string{"name"}, FieldDocs: name,
		},
		{
			Name:  "delete_tenant",
			Title: "Delete workspace",
			Description: "Permanently delete a workspace and everything in it: monitors and their history, alerts, " +
				"incidents, status pages, channels, SLAs, API keys and memberships. Access stops at once, the data is " +
				"purged in the background, and it cannot be restored. confirm_name must repeat the workspace's name. " +
				"The default workspace (and the one new SSO users join) and the last workspace cannot be deleted. " +
				"Superadmins only.",
			Method: http.MethodDelete, Route: "/tenants/{id}", Platform: true,
			PathDocs: workspace, Destructive: true,
			Body: typeOf[models.DeleteWorkspaceRequest](), Required: []string{"confirm_name"},
			FieldDocs: map[string]string{"confirm_name": "The workspace's current name, repeated to confirm."},
		},
	}
}
