package tenants

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/tenancy"
)

func TestNormalizeTenantName(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"  Acme  ", "Acme", true},
		{"Équipe données", "Équipe données", true},
		{strings.Repeat("é", 100), strings.Repeat("é", 100), true},
		{strings.Repeat("a", 101), "", false},
		{"   ", "", false},
		{"bad\nname", "", false},
		{"tab\tname", "", false},
	} {
		got, err := NormalizeTenantName(tc.in)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("NormalizeTenantName(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidName) {
			t.Errorf("NormalizeTenantName(%q) error = %v; want ErrInvalidName", tc.in, err)
		}
	}
}

func TestProtectedTenants(t *testing.T) {
	jit := uuid.New()
	svc := NewService(nil).WithProtectedTenants(jit.String(), "", "not-a-uuid")
	if !svc.isProtected(uuid.MustParse(tenancy.DefaultTenantID)) || !svc.isProtected(jit) {
		t.Fatal("default and JIT workspaces must be protected")
	}
	if svc.isProtected(uuid.New()) {
		t.Fatal("other workspaces are deletable")
	}
	if !NewService(nil).isProtected(uuid.MustParse(tenancy.DefaultTenantID)) {
		t.Fatal("the default workspace is protected without configuration")
	}
}
