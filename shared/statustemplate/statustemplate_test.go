package statustemplate

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultSourceParses(t *testing.T) {
	if _, err := New("default", DefaultSource); err != nil {
		t.Fatalf("DefaultSource must parse: %v", err)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		wantErr bool
	}{
		{"default template", DefaultSource, false},
		{"minimal custom template", `<html><body>{{.Title}}</body></html>`, false},
		{"uses funcmap", `{{typeIcon "http"}} {{globalStripCells "24h"}}`, false},
		{"empty", "", true},
		{"whitespace only", "   \n\t", true},
		{"unclosed action", `{{if .Title}}no end`, true},
		{"unknown function", `{{nosuchfunc .Title}}`, true},
		{"oversize", strings.Repeat("x", MaxSourceSize+1), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.source)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestStripCells(t *testing.T) {
	tests := []struct {
		rangeName                 string
		global, monitor, expanded int
	}{
		{"1h", 60, 30, 60},
		{"24h", 48, 36, 36},
		{"7d", 70, 36, 36},
		{"30d", 90, 60, 90},
		{"90d", 90, 60, 90},
		{"unknown", 48, 36, 36},
	}
	for _, tt := range tests {
		if got := GlobalStripCells(tt.rangeName); got != tt.global {
			t.Errorf("GlobalStripCells(%q) = %d, want %d", tt.rangeName, got, tt.global)
		}
		if got := MonitorStripCells(tt.rangeName); got != tt.monitor {
			t.Errorf("MonitorStripCells(%q) = %d, want %d", tt.rangeName, got, tt.monitor)
		}
		if got := ExpandedStripCells(tt.rangeName); got != tt.expanded {
			t.Errorf("ExpandedStripCells(%q) = %d, want %d", tt.rangeName, got, tt.expanded)
		}
	}
	// The 1h strips resample 60 one-minute buckets; anything that does not
	// divide 60 would draw some minutes wider than others.
	for _, cells := range []int{ShortRangeGlobalStripCells, ShortRangeMonitorStripCells, ShortRangeExpandedStripCells} {
		if 60%cells != 0 {
			t.Errorf("1h strip cell count %d does not divide 60 minute buckets", cells)
		}
	}
}

func TestValidateParseErrorIncludesLine(t *testing.T) {
	err := Validate("line one\n{{.Broken")
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Fatalf("parse error should reference line 2 for the editor, got: %v", err)
	}
}

func TestPreviewToken(t *testing.T) {
	const secret = "test-secret"
	const pageID = "3d4c2f10-0000-0000-0000-000000000000"
	now := time.Now()

	token := MintPreviewToken(secret, pageID, now.Add(time.Hour))
	if !VerifyPreviewToken(secret, pageID, token, now) {
		t.Fatal("freshly minted token must verify")
	}
	if VerifyPreviewToken(secret, "other-page", token, now) {
		t.Fatal("token must be bound to its page ID")
	}
	if VerifyPreviewToken("other-secret", pageID, token, now) {
		t.Fatal("token must not verify with a different secret")
	}
	if VerifyPreviewToken(secret, pageID, token, now.Add(2*time.Hour)) {
		t.Fatal("expired token must not verify")
	}
	if VerifyPreviewToken(secret, pageID, "garbage", now) {
		t.Fatal("malformed token must not verify")
	}
}
