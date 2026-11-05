package validation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequestSecretFieldValidation(t *testing.T) {
	cases := []struct {
		name      string
		validator interface {
			ValidateConfig(json.RawMessage) error
		}
		config  string
		wantErr string // "" = valid
	}{
		{"http secret headers and basic auth", &HTTPConfigValidator{},
			`{"url":"https://x.test","method":"GET","headers":{"Accept":"*/*"},"secret_headers":{"X-Api-Key":"***"},"basic_auth_username":"ops","basic_auth_password":"***"}`, ""},
		{"http name in both maps", &HTTPConfigValidator{},
			`{"url":"https://x.test","method":"GET","headers":{"x-api-key":"a"},"secret_headers":{"X-Api-Key":"b"}}`, "both headers and secret_headers"},
		{"http CRLF in secret header value", &HTTPConfigValidator{},
			`{"url":"https://x.test","method":"GET","secret_headers":{"X-Api-Key":"a\r\nX-Evil: 1"}}`, "line breaks"},
		{"http CRLF in header name", &HTTPConfigValidator{},
			`{"url":"https://x.test","method":"GET","headers":{"X-A\nB":"a"}}`, "line breaks"},
		{"http basic auth with Authorization header", &HTTPConfigValidator{},
			`{"url":"https://x.test","method":"GET","basic_auth_username":"ops","basic_auth_password":"p","secret_headers":{"authorization":"Bearer x"}}`, "basic auth and an Authorization header"},
		{"http password without username", &HTTPConfigValidator{},
			`{"url":"https://x.test","method":"GET","basic_auth_password":"p"}`, "requires basic_auth_username"},
		{"http username with colon", &HTTPConfigValidator{},
			`{"url":"https://x.test","method":"GET","basic_auth_username":"a:b"}`, "cannot contain ':'"},

		{"grpc metadata", &GRPCConfigValidator{},
			`{"host":"g.test","metadata":{"x-tenant":"t"},"secret_metadata":{"authorization":"***"}}`, ""},
		{"grpc metadata in both maps", &GRPCConfigValidator{},
			`{"host":"g.test","metadata":{"Authorization":"a"},"secret_metadata":{"authorization":"b"}}`, "both metadata and secret_metadata"},
		{"grpc reserved key", &GRPCConfigValidator{},
			`{"host":"g.test","metadata":{"grpc-timeout":"1S"}}`, "reserved grpc- prefix"},
		{"grpc bad key", &GRPCConfigValidator{},
			`{"host":"g.test","secret_metadata":{"bad key":"v"}}`, "may only contain"},

		{"synthetic api secret ref", &SyntheticAPIConfigValidator{},
			`{"variables":{"host":"x"},"secret_variables":{"token":"***"},"steps":[{"id":"a","request":{"method":"GET","url":"https://x.test/{{host}}","headers":{"Authorization":"Bearer {{token}}"}}},{"id":"b","request":{"method":"GET","url":"https://x.test/{{ session }}"},"extract":[{"name":"session","from":"json","path":"s"}]}]}`, ""},
		{"synthetic api undefined ref", &SyntheticAPIConfigValidator{},
			`{"steps":[{"id":"a","request":{"method":"POST","url":"https://x.test","body":"{\"t\":\"{{tokn}}\"}"}}]}`, `undefined variable "tokn"`},
		{"synthetic api variable in both maps", &SyntheticAPIConfigValidator{},
			`{"variables":{"token":"a"},"secret_variables":{"token":"b"},"steps":[{"id":"a","request":{"method":"GET","url":"https://x.test"}}]}`, "both variables and secret_variables"},
		{"synthetic api bad secret name", &SyntheticAPIConfigValidator{},
			`{"secret_variables":{"to ken":"b"},"steps":[{"id":"a","request":{"method":"GET","url":"https://x.test"}}]}`, "may only contain"},

		{"synthetic browser secret password", &SyntheticBrowserConfigValidator{},
			`{"start_url":"https://x.test/login","variables":{"username":"u"},"secret_variables":{"password":"***"},"steps":[{"id":"u","action":"fill","selector":"#u","value":"{{username}}"},{"id":"p","action":"fill","selector":"#p","value":"{{password}}"}]}`, ""},
		{"synthetic browser undefined ref", &SyntheticBrowserConfigValidator{},
			`{"start_url":"https://x.test/login","steps":[{"id":"p","action":"fill","selector":"#p","value":"{{password}}"}]}`, `undefined variable "password"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.validator.ValidateConfig(json.RawMessage(tc.config))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
