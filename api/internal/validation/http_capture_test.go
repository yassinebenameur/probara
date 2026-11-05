package validation

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestHTTPCaptureValidation(t *testing.T) {
	for _, tt := range []struct {
		config  string
		wantErr bool
	}{
		{`{"url":"https://a.example","method":"GET","capture_failure_response":true}`, false},
		{`{"url":"https://a.example","method":"GET","capture_failure_response":true,"capture_body_bytes":8192}`, false},
		{`{"url":"https://a.example","method":"GET","capture_failure_response":true,"capture_body_bytes":8193}`, true},
		{`{"url":"https://a.example","method":"GET","capture_failure_response":true,"capture_body_bytes":0}`, true},
	} {
		err := DefaultRegistry.Validate(models.MonitorTypeHTTP, json.RawMessage(tt.config))
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tt.config, err, tt.wantErr)
		}
	}
}
