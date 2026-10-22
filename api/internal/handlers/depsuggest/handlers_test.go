package depsuggest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/logger"
)

// busyError returns the error the local AI client produces for a service
// that answers 429 with the given Retry-After header ("" for none).
func busyError(t *testing.T, retryAfter string) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	_, err := ai.Probe(context.Background(), ai.Config{BaseURL: srv.URL, APIKey: "handler-test-token-0123456789"})
	require.ErrorIs(t, err, ai.ErrUnavailable)
	return err
}

func TestWriteSuggestError(t *testing.T) {
	log := logger.New("test", "error")
	cases := []struct {
		name       string
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{"not configured", ai.ErrNotConfigured, http.StatusServiceUnavailable, "not_configured", ""},
		{"busy with the service's Retry-After", fmt.Errorf("suggest: %w", busyError(t, "7")), http.StatusServiceUnavailable, "unavailable", "7"},
		{"busy without Retry-After", busyError(t, ""), http.StatusServiceUnavailable, "unavailable", "5"},
		{"other failure", errors.New("co-firing query: boom"), http.StatusInternalServerError, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeSuggestError(rec, tc.err, log)
			require.Equal(t, tc.status, rec.Code)
			require.Equal(t, tc.retryAfter, rec.Header().Get("Retry-After"))
			if tc.code != "" {
				var body struct {
					Error string `json:"error"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				require.Equal(t, tc.code, body.Error)
			}
		})
	}
}
