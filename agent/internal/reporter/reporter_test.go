package reporter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/agent/internal/models"
)

func TestReportReturnsRemoteDisabledWhenServerReturnsGone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "agent monitor deleted", http.StatusGone)
	}))
	defer server.Close()

	rep := NewReporter(server.URL, "agent-1", "api-key")
	err := rep.Report(context.Background(), &models.AgentMetrics{Timestamp: time.Now()})
	if !errors.Is(err, ErrRemoteDisabled) {
		t.Fatalf("expected ErrRemoteDisabled, got %v", err)
	}
}
