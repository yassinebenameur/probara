package oidcauth

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/auth"
)

func TestParseGroupsClaim(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"absent", "", nil},
		{"array", `["ops","devs"]`, []string{"ops", "devs"}},
		{"single string", `"ops"`, []string{"ops"}},
		{"dedup and trim", `[" ops ","ops",""]`, []string{"ops"}},
		{"empty array", `[]`, nil},
		{"garbage object", `{"a":1}`, nil},
		{"number", `42`, nil},
		{"mixed-type array", `["ops",42]`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGroupsClaim(json.RawMessage(tt.raw))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseGroupsClaim(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestResolveMappedRoles(t *testing.T) {
	tenantA := uuid.New()
	tenantB := uuid.New()
	mappings := []GroupMapping{
		{GroupName: "platform-admins", TenantID: nil, Role: auth.PlatformRoleSuperadmin},
		{GroupName: "a-admins", TenantID: &tenantA, Role: auth.RoleAdmin},
		{GroupName: "a-viewers", TenantID: &tenantA, Role: auth.RoleViewer},
		{GroupName: "b-editors", TenantID: &tenantB, Role: auth.RoleEditor},
	}

	tests := []struct {
		name         string
		groups       []string
		wantPlatform string
		wantRoles    map[uuid.UUID]string
	}{
		{
			name:         "no groups",
			groups:       nil,
			wantPlatform: auth.PlatformRoleMember,
			wantRoles:    map[uuid.UUID]string{},
		},
		{
			name:         "unmapped groups only",
			groups:       []string{"unrelated"},
			wantPlatform: auth.PlatformRoleMember,
			wantRoles:    map[uuid.UUID]string{},
		},
		{
			name:         "platform mapping grants superadmin",
			groups:       []string{"platform-admins"},
			wantPlatform: auth.PlatformRoleSuperadmin,
			wantRoles:    map[uuid.UUID]string{},
		},
		{
			name:         "highest role wins per tenant",
			groups:       []string{"a-viewers", "a-admins"},
			wantPlatform: auth.PlatformRoleMember,
			wantRoles:    map[uuid.UUID]string{tenantA: auth.RoleAdmin},
		},
		{
			name:         "multi-tenant",
			groups:       []string{"a-viewers", "b-editors"},
			wantPlatform: auth.PlatformRoleMember,
			wantRoles:    map[uuid.UUID]string{tenantA: auth.RoleViewer, tenantB: auth.RoleEditor},
		},
		{
			name:         "case-sensitive match",
			groups:       []string{"A-Admins"},
			wantPlatform: auth.PlatformRoleMember,
			wantRoles:    map[uuid.UUID]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform, roles := resolveMappedRoles(tt.groups, mappings)
			if platform != tt.wantPlatform {
				t.Fatalf("platform = %q, want %q", platform, tt.wantPlatform)
			}
			if !reflect.DeepEqual(roles, tt.wantRoles) {
				t.Fatalf("roles = %v, want %v", roles, tt.wantRoles)
			}
		})
	}
}

func TestDiffMemberships(t *testing.T) {
	tenantA := uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	tenantB := uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	tenantC := uuid.MustParse("00000000-0000-0000-0000-00000000000c")

	result := &RoleSyncResult{}
	diffMemberships(result,
		map[uuid.UUID]string{tenantA: auth.RoleViewer, tenantB: auth.RoleEditor},
		map[uuid.UUID]string{tenantA: auth.RoleAdmin, tenantC: auth.RoleViewer},
	)

	if want := []string{tenantC.String() + ":viewer"}; !reflect.DeepEqual(result.AddedMemberships, want) {
		t.Fatalf("added = %v, want %v", result.AddedMemberships, want)
	}
	if want := []string{tenantB.String() + ":editor"}; !reflect.DeepEqual(result.RemovedMemberships, want) {
		t.Fatalf("removed = %v, want %v", result.RemovedMemberships, want)
	}
	if want := []string{tenantA.String() + ":viewer→admin"}; !reflect.DeepEqual(result.ChangedMemberships, want) {
		t.Fatalf("changed = %v, want %v", result.ChangedMemberships, want)
	}
}
