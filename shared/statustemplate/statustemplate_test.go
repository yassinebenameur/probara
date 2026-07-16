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
