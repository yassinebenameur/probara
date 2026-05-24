package plugin

import (
	"context"
	"encoding/json"
	"testing"
)

type stubPlugin struct{ m Manifest }

func (s *stubPlugin) Manifest() Manifest                              { return s.m }
func (s *stubPlugin) Validate(json.RawMessage) error                  { return nil }
func (s *stubPlugin) Send(context.Context, DispatchRequest) error     { return nil }

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	p := &stubPlugin{m: Manifest{Type: "demo", DisplayName: "Demo", Version: "1"}}

	r.Register(p)

	if !r.Has("demo") {
		t.Fatal("expected Has(\"demo\") to be true")
	}
	got, ok := r.Get("demo")
	if !ok || got != p {
		t.Fatalf("Get(\"demo\") = (%v, %v), want (%v, true)", got, ok, p)
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get(\"missing\") should not find anything")
	}
}

func TestRegistry_TypesAndAllSorted(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubPlugin{m: Manifest{Type: "bravo", DisplayName: "B", Version: "1"}})
	r.Register(&stubPlugin{m: Manifest{Type: "alpha", DisplayName: "A", Version: "1"}})

	types := r.Types()
	if len(types) != 2 || types[0] != "alpha" || types[1] != "bravo" {
		t.Fatalf("Types() = %v, want [alpha bravo]", types)
	}
	manifests := r.All()
	if len(manifests) != 2 || manifests[0].Type != "alpha" || manifests[1].Type != "bravo" {
		t.Fatalf("All() out of order: %v", manifests)
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	r := NewRegistry()
	p := &stubPlugin{m: Manifest{Type: "dup", DisplayName: "D", Version: "1"}}
	r.Register(p)

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	r.Register(p)
}

func TestRegistry_NilAndEmptyTypePanic(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic on nil plugin")
			}
		}()
		NewRegistry().Register(nil)
	})
	t.Run("empty type", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic on empty Type")
			}
		}()
		NewRegistry().Register(&stubPlugin{m: Manifest{}})
	})
}

func TestManifest_HasCapability(t *testing.T) {
	m := Manifest{Capabilities: []Capability{CapabilityRenderedAlert, CapabilityTestable}}
	if !m.HasCapability(CapabilityRenderedAlert) {
		t.Error("expected RenderedAlert")
	}
	if m.HasCapability(CapabilityRawEvent) {
		t.Error("did not expect RawEvent")
	}
}
