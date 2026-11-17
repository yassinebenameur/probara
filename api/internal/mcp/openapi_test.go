package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestSuccessResponsesNameOperations keeps the response table keyed by real
// operation IDs (Bind refuses a stale key too, but this needs no router).
func TestSuccessResponsesNameOperations(t *testing.T) {
	names := map[string]bool{}
	for _, s := range catalog() {
		names[s.Name] = true
	}
	for _, d := range openAPIRoutes() {
		if names[d.Name] {
			t.Errorf("%s: documented route reuses a catalog tool name", d.Name)
		}
		names[d.Name] = true
	}
	for name, r := range successResponses {
		if !names[name] {
			t.Errorf("successResponses[%q]: no such operation", name)
		}
		if r.Body != nil {
			if _, err := responseSchema(r.Body); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
	t.Logf("%d of %d operations have a described success response", len(successResponses), len(names))
}

type selfMarshaling struct{ v int }

func (s selfMarshaling) MarshalJSON() ([]byte, error) { return json.Marshal(s.v) }

// TestResponseSchemaRefusesWhatReflectionCannotShow: a type that writes its
// own JSON, or a field reflection cannot describe, fails instead of being
// documented wrongly or dropped.
func TestResponseSchemaRefusesWhatReflectionCannotShow(t *testing.T) {
	type wrapsMarshaler struct {
		Value *selfMarshaling `json:"value"`
	}
	if _, err := responseSchema(reflect.TypeFor[wrapsMarshaler]()); err == nil || !strings.Contains(err.Error(), "marshals itself") {
		t.Errorf("self-marshaling field: err = %v", err)
	}
	type hasFunc struct {
		F func() `json:"f"`
	}
	if _, err := responseSchema(reflect.TypeFor[hasFunc]()); err == nil {
		t.Error("a func field must fail, not vanish from the schema")
	}
	type fine struct {
		At   *time.Time `json:"at"`
		Note string     `json:"note,omitempty"`
	}
	s, err := responseSchema(reflect.TypeFor[fine]())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(s)
	if strings.Contains(string(raw), `"additionalProperties":false`) {
		t.Errorf("response objects must stay open: %s", raw)
	}
	if len(s.Required) != 1 || s.Required[0] != "at" {
		t.Errorf("required = %v, want [at] (fields without omitempty are always written)", s.Required)
	}
}
