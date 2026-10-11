package ai

import (
	"context"
	"errors"
	"testing"
)

// The exported constructors wrap newLocalAnalyzer; on any validation failure
// they must hand back a nil interface (not a typed-nil *localAnalyzer), and on
// success a usable value, exactly as before the assertion was removed.
func TestConstructorsReturnNilInterfaceOnError(t *testing.T) {
	bad := []struct {
		name string
		cfg  Config
		want error
	}{
		{"disabled", Config{Disabled: true}, ErrNotConfigured},
		{"bad scheme", Config{BaseURL: "ftp://ai.local", APIKey: "test-internal-token"}, nil},
		{"short token", Config{BaseURL: "http://ai.local", APIKey: "short"}, nil},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			a, err := NewAnalyzer(tc.cfg)
			if err == nil || a != nil {
				t.Fatalf("NewAnalyzer = (%v, %v), want (nil, error)", a, err)
			}
			d, err := NewDependencyAdvisor(tc.cfg)
			if err == nil || d != nil {
				t.Fatalf("NewDependencyAdvisor = (%v, %v), want (nil, error)", d, err)
			}
			if _, perr := Probe(context.Background(), tc.cfg); perr == nil {
				t.Fatal("Probe accepted an invalid config")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	good := Config{BaseURL: "http://ai.local", APIKey: "test-internal-token"}
	if a, err := NewAnalyzer(good); err != nil || a == nil {
		t.Fatalf("NewAnalyzer(valid) = (%v, %v)", a, err)
	}
	if d, err := NewDependencyAdvisor(good); err != nil || d == nil {
		t.Fatalf("NewDependencyAdvisor(valid) = (%v, %v)", d, err)
	}
}
