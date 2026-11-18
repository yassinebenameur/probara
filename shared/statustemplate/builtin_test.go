package statustemplate

import (
	"strings"
	"testing"
)

// Every built-in parses, passes the same validation as an uploaded template,
// and runs its inline scripts under the CSP nonce.
func TestBuiltinsParseValidateAndCarryNonce(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range Builtins() {
		if b.Name == "" || b.Label == "" || strings.TrimSpace(b.Source) == "" {
			t.Fatalf("incomplete built-in %+v", b.Name)
		}
		if seen[b.Name] {
			t.Fatalf("duplicate built-in name %q", b.Name)
		}
		seen[b.Name] = true
		if err := Validate(b.Source); err != nil {
			t.Fatalf("built-in %q fails Validate: %v", b.Name, err)
		}
		scripts := strings.Count(strings.ToLower(b.Source), "<script")
		if scripts == 0 || scripts != strings.Count(b.Source, `<script nonce="{{cspNonce}}"`) {
			t.Fatalf("built-in %q has %d <script> elements; every one needs nonce=\"{{cspNonce}}\"", b.Name, scripts)
		}
		// No inline handlers and no external resources: the strict CSP
		// allows neither.
		for _, banned := range []string{" onclick=", " onload=", " onerror=", " onsubmit=", "<link rel=\"stylesheet\"", "@import", "src=\"http", "url(http"} {
			if b.Name == BuiltinClassic && strings.Contains(b.Source, banned) {
				t.Fatalf("built-in %q contains %q", b.Name, banned)
			}
		}
	}
	if Builtins()[0].Name != BuiltinDefault {
		t.Fatalf("the default template must come first")
	}
	if !seen[BuiltinClassic] {
		t.Fatalf("classic is not registered")
	}
}

func TestLookupAndNormalizeBuiltin(t *testing.T) {
	for _, tt := range []struct {
		in       string
		found    bool
		resolved string
	}{
		{"default", true, BuiltinDefault},
		{"classic", true, BuiltinClassic},
		{" Classic ", true, BuiltinClassic},
		{"", false, BuiltinDefault},
		{"modern", false, BuiltinDefault},
	} {
		if _, ok := LookupBuiltin(tt.in); ok != tt.found {
			t.Errorf("LookupBuiltin(%q) found = %v, want %v", tt.in, ok, tt.found)
		}
		if got := NormalizeBuiltin(tt.in); got != tt.resolved {
			t.Errorf("NormalizeBuiltin(%q) = %q, want %q", tt.in, got, tt.resolved)
		}
	}
	if got := strings.Join(BuiltinNames(), ","); got != "default,classic" {
		t.Fatalf("BuiltinNames() = %s", got)
	}
}

// The classic template carries the same brand mark data URI as the default
// (CLAUDE.md: the brand mark's copies change together).
func TestClassicFaviconMatchesDefault(t *testing.T) {
	icon := func(src string) string {
		start := strings.Index(src, `<link rel="icon" href="`)
		if start < 0 {
			t.Fatal("no favicon link")
		}
		rest := src[start:]
		return rest[:strings.Index(rest, "/>")]
	}
	if icon(ClassicSource) != icon(DefaultSource) {
		t.Fatal("classic favicon differs from the default template's")
	}
}
