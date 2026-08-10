package models

import (
	"time"

	"github.com/google/uuid"
)

// OIDCGroupMapping maps an IdP group to a role. A nil TenantID is a
// platform-level mapping (role "superadmin"); otherwise Role is a tenant
// role on TenantID. Any existing rows make the IdP the source of truth for
// SSO users' roles: they are re-synced on every OIDC login.
type OIDCGroupMapping struct {
	ID        uuid.UUID `json:"id"`
	GroupName string    `json:"group_name"`
	// Label is an optional operator-facing display name. Cosmetic only —
	// matching always uses GroupName (Azure emits GUIDs there).
	Label    *string    `json:"label,omitempty"`
	TenantID *uuid.UUID `json:"tenant_id,omitempty"`
	// TenantName is resolved for display; nil for platform mappings and for
	// rows whose tenant vanished mid-request.
	TenantName *string   `json:"tenant_name,omitempty"`
	Role       string    `json:"role"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CreateOIDCGroupMappingRequest creates a mapping. Omit tenant_id for a
// platform mapping (role must be "superadmin").
type CreateOIDCGroupMappingRequest struct {
	GroupName string  `json:"group_name"`
	Label     string  `json:"label"`
	TenantID  *string `json:"tenant_id"`
	Role      string  `json:"role"`
}

// UpdateOIDCGroupMappingRequest changes a mapping's role and/or label
// (omitted fields keep their value; an empty label clears it). Retargeting a
// mapping (group or tenant) is delete + create.
type UpdateOIDCGroupMappingRequest struct {
	Role  *string `json:"role,omitempty"`
	Label *string `json:"label,omitempty"`
}

// OIDCSeenGroup is an IdP group observed in a verified ID token at a
// successful SSO login — the mapping editor's suggestion source, since OIDC
// has no API to enumerate an IdP's groups.
type OIDCSeenGroup struct {
	GroupName string    `json:"group_name"`
	FirstSeen time.Time `json:"first_seen_at"`
	LastSeen  time.Time `json:"last_seen_at"`
}

// OIDCGroupMappingListResponse carries the mappings plus the OIDC config
// facts the settings UI needs to warn about misconfiguration.
type OIDCGroupMappingListResponse struct {
	Mappings []OIDCGroupMapping `json:"mappings"`
	// SeenGroups are groups presented at past SSO logins, most recent first.
	SeenGroups []OIDCSeenGroup `json:"seen_groups"`
	// GroupsClaim is the ID-token claim the sync reads (OIDC_GROUPS_CLAIM).
	GroupsClaim string `json:"groups_claim"`
	// GroupsScopeRequested reports whether "groups" is in OIDC_SCOPES — when
	// false and mappings exist, many IdPs will never send the claim.
	GroupsScopeRequested bool `json:"groups_scope_requested"`
}
