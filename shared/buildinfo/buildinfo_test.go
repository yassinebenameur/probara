package buildinfo

import "testing"

func TestReleaseVersion(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	cases := []struct {
		version string
		want    string
		ok      bool
	}{
		{"dev", "", false},
		{"", "", false},
		{"main", "", false},
		{"sha-42f983a", "", false},
		{"2.0.0", "2.0.0", true},
		{"v2.0.0", "2.0.0", true},
		{"2.0.0-alpha.10", "2.0.0-alpha.10", true},
		{"v1.0.0-beta.2", "1.0.0-beta.2", true},
		{"2.0", "", false},
		{"2.0.0+build.5", "", false},
	}
	for _, tc := range cases {
		Version = tc.version
		got, ok := ReleaseVersion()
		if got != tc.want || ok != tc.ok {
			t.Errorf("ReleaseVersion() with Version=%q = (%q, %v), want (%q, %v)", tc.version, got, ok, tc.want, tc.ok)
		}
	}
}

func TestDefaultIsDev(t *testing.T) {
	if Version != "dev" {
		t.Fatalf("unstamped Version = %q, want dev", Version)
	}
}
