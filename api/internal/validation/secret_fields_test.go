package validation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
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
		{"http one secret header spelled twice", &HTTPConfigValidator{},
			`{"url":"https://x.test","method":"GET","secret_headers":{"Authorization":"a","authorization":"b"}}`, "name the same header"},
		{"http empty header name", &HTTPConfigValidator{},
			`{"url":"https://x.test","method":"GET","secret_headers":{" ":"a"}}`, "names cannot be empty"},

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
		{"synthetic api variable in both maps", &SyntheticAPIConfigValidator{},
			`{"variables":{"token":"a"},"secret_variables":{"token":"b"},"steps":[{"id":"a","request":{"method":"GET","url":"https://x.test"}}]}`, "both variables and secret_variables"},
		{"synthetic api secret names differing by case", &SyntheticAPIConfigValidator{},
			`{"secret_variables":{"Token":"a","token":"b"},"steps":[{"id":"a","request":{"method":"GET","url":"https://x.test"}}]}`, "differ only by case"},
		{"synthetic browser secret password", &SyntheticBrowserConfigValidator{},
			`{"start_url":"https://x.test/login","variables":{"username":"u"},"secret_variables":{"password":"***"},"steps":[{"id":"p","action":"fill","selector":"#p","value":"{{password}}"}]}`, ""},
		{"synthetic browser variable in both maps", &SyntheticBrowserConfigValidator{},
			`{"start_url":"https://x.test","variables":{"password":"a"},"secret_variables":{"password":"b"},"steps":[{"id":"a","action":"goto","url":"https://x.test"}]}`, "both variables and secret_variables"},
		{"synthetic browser secret names differing by case", &SyntheticBrowserConfigValidator{},
			`{"start_url":"https://x.test","secret_variables":{"PW":"a","pw":"b"},"steps":[{"id":"a","action":"goto","url":"https://x.test"}]}`, "differ only by case"},
		{"synthetic api padded secret name", &SyntheticAPIConfigValidator{},
			`{"secret_variables":{" token":"b"},"steps":[{"id":"a","request":{"method":"GET","url":"https://x.test"}}]}`, "may only contain"},
		{"synthetic api bad secret name", &SyntheticAPIConfigValidator{},
			`{"secret_variables":{"to ken":"b"},"steps":[{"id":"a","request":{"method":"GET","url":"https://x.test"}}]}`, "may only contain"},
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

// TestSecretKeySpelling: a miscapitalized secret key would be honoured by the
// case-insensitive struct decode yet skipped by exact-key encryption and
// masking, so the registry refuses it for every secret-bearing type.
func TestSecretKeySpelling(t *testing.T) {
	cases := []struct {
		monitorType models.MonitorType
		config      string
		wantErr     string
	}{
		{models.MonitorTypeHTTP, `{"url":"https://x.test","method":"GET","Secret_Headers":{"X-Api-Key":"k"}}`, `"Secret_Headers" must be spelled "secret_headers"`},
		{models.MonitorTypeHTTP, `{"url":"https://x.test","method":"GET","basic_auth_username":"u","Basic_Auth_Password":"pw"}`, `must be spelled "basic_auth_password"`},
		{models.MonitorTypePostgres, `{"host":"db.test","Password":"pw"}`, `must be spelled "password"`},
		{models.MonitorTypeWebSocket, `{"url":"wss://x.test","HEADERS":{"a":"b"}}`, `must be spelled "headers"`},
		{models.MonitorTypeGRPC, `{"host":"g.test","Secret_Metadata":{"a":"b"}}`, `must be spelled "secret_metadata"`},
		{models.MonitorTypeHTTP, `{"url":"https://x.test","method":"GET","basic_auth_username":"u","basic_auth_password":"pw"}`, ""},
		// Plain keys the header move reads: a miscased spelling would carry a
		// credential past it in plain text, at every level the worker decodes.
		{models.MonitorTypeHTTP, `{"url":"https://x.test","method":"GET","Headers":{"Authorization":"Bearer x"}}`, `"Headers" must be spelled "headers"`},
		{models.MonitorTypeGRPC, `{"host":"g.test","Metadata":{"authorization":"Bearer x"}}`, `"Metadata" must be spelled "metadata"`},
		{models.MonitorTypeSyntheticAPI, `{"Variables":{"password":"p"},"steps":[{"id":"a","request":{"method":"GET","url":"https://x.test"}}]}`, `"Variables" must be spelled "variables"`},
		{models.MonitorTypeSyntheticAPI, `{"Steps":[{"id":"a","request":{"method":"GET","url":"https://x.test"}}]}`, `"Steps" must be spelled "steps"`},
		{models.MonitorTypeSyntheticAPI, `{"steps":[{"id":"a","Request":{"method":"GET","url":"https://x.test"}}]}`, `"steps[0].Request" must be spelled "steps[0].request"`},
		{models.MonitorTypeSyntheticAPI, `{"steps":[{"id":"a","request":{"method":"GET","url":"https://x.test"}},{"id":"b","request":{"method":"GET","url":"https://x.test","Headers":{"Authorization":"x"}}}]}`, `"steps[1].request.Headers" must be spelled "steps[1].request.headers"`},
		{models.MonitorTypeSyntheticBrowser, `{"start_url":"https://x.test","Variables":{"password":"p"},"steps":[{"id":"a","action":"goto","url":"https://x.test"}]}`, `"Variables" must be spelled "variables"`},
		{models.MonitorTypeSyntheticBrowser, `{"start_url":"https://x.test","Secret_Variables":{"password":"p"},"steps":[{"id":"a","action":"goto","url":"https://x.test"}]}`, `"Secret_Variables" must be spelled "secret_variables"`},
		{models.MonitorTypeSyntheticAPI, `{"variables":{"a":"b"},"steps":[{"id":"a","request":{"method":"GET","url":"https://x.test","headers":{"Accept":"*/*"}}}]}`, ""},
		// Types without secrets are left to their own validator.
		{models.MonitorTypePing, `{"host":"x.test","Password":"pw"}`, ""},
	}
	for _, tc := range cases {
		err := DefaultRegistry.Validate(tc.monitorType, json.RawMessage(tc.config))
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s %s: unexpected error %v", tc.monitorType, tc.config, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s %s: error = %v, want %q", tc.monitorType, tc.config, err, tc.wantErr)
		}
	}
}

// TestValidateMergedSecretPairs covers configs that only conflict once the
// write-only merge restored a stored secret the request left out.
func TestValidateMergedSecretPairs(t *testing.T) {
	cases := []struct {
		monitorType models.MonitorType
		merged      string
		wantErr     string
	}{
		// Basic auth added while the stored Authorization secret header was kept.
		{models.MonitorTypeHTTP, `{"url":"https://x.test","method":"GET","basic_auth_username":"ops","secret_headers":{"Authorization":"enc"}}`, "basic auth and an Authorization header"},
		// A plain header added beside a kept secret one of the same name.
		{models.MonitorTypeHTTP, `{"url":"https://x.test","method":"GET","headers":{"X-Custom":"a"},"secret_headers":{"x-custom":"enc"}}`, "both headers and secret_headers"},
		// Username removed while the stored password was kept.
		{models.MonitorTypeHTTP, `{"url":"https://x.test","method":"GET","basic_auth_password":"enc"}`, "requires basic_auth_username"},
		{models.MonitorTypeGRPC, `{"host":"g.test","metadata":{"Tenant":"a"},"secret_metadata":{"tenant":"enc"}}`, "both metadata and secret_metadata"},
		{models.MonitorTypeSyntheticAPI, `{"variables":{"token":"a"},"secret_variables":{"token":"enc"},"steps":[]}`, "both variables and secret_variables"},
		{models.MonitorTypeHTTP, `{"url":"https://x.test","method":"GET","headers":{"Accept":"*/*"},"secret_headers":{"Authorization":"enc"}}`, ""},
		{models.MonitorTypeSyntheticBrowser, `{"start_url":"https://x.test","variables":{"pw":"a"},"secret_variables":{"pw":"enc"},"steps":[]}`, "both variables and secret_variables"},
		{models.MonitorTypePing, `{"host":"x"}`, ""},
	}
	for _, tc := range cases {
		err := ValidateMergedSecretPairs(tc.monitorType, json.RawMessage(tc.merged))
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s %s: unexpected error %v", tc.monitorType, tc.merged, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s %s: error = %v, want %q", tc.monitorType, tc.merged, err, tc.wantErr)
		}
	}
}
