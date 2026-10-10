package secrets

import (
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

func TestMergePreserveSecrets_ClearAndKeep(t *testing.T) {
	manifest := plugin.Manifest{Fields: []plugin.Field{
		{Key: "url"},
		{Key: "custom_headers", Secret: true},
	}}
	stored := map[string]any{"url": "https://old.example.com", "custom_headers": "enc:v1:stored"}

	cases := []struct {
		name     string
		incoming map[string]any
		want     any // nil = key must be absent
	}{
		{"absent keeps", map[string]any{"url": "https://new.example.com"}, "enc:v1:stored"},
		{"blank keeps", map[string]any{"custom_headers": ""}, "enc:v1:stored"},
		{"mask keeps", map[string]any{"custom_headers": MaskedSecret}, "enc:v1:stored"},
		{"null clears", map[string]any{"custom_headers": nil}, nil},
		{"value replaces", map[string]any{"custom_headers": `{"X-A":"1"}`}, `{"X-A":"1"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MergePreserveSecrets(manifest, tc.incoming, stored)
			v, ok := got["custom_headers"]
			if tc.want == nil {
				if ok {
					t.Fatalf("custom_headers = %v, want absent", v)
				}
				return
			}
			if v != tc.want {
				t.Fatalf("custom_headers = %v, want %v", v, tc.want)
			}
		})
	}
}
