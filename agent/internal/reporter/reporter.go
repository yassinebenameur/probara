package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/yassinebenameur/probara/agent/internal/models"
)

// Reporter sends metrics to the backend
type Reporter struct {
	backendURL string
	agentID    string
	apiKey     string
	client     *http.Client
}

// NewReporter creates a new metrics reporter
func NewReporter(backendURL, agentID, apiKey string) *Reporter {
	return &Reporter{
		backendURL: backendURL,
		agentID:    agentID,
		apiKey:     apiKey,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Report sends metrics to the backend with retry logic
func (r *Reporter) Report(ctx context.Context, metrics *models.AgentMetrics) error {
	payload := models.AgentMetricsPayload{
		AgentID: r.agentID,
		Metrics: *metrics,
	}

	// Try to send with exponential backoff
	maxRetries := 3
	backoff := time.Second

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
			}
		}

		err := r.send(ctx, payload)
		if err == nil {
			return nil
		}
		lastErr = err
	}

	return fmt.Errorf("failed to report metrics after %d attempts: %w", maxRetries, lastErr)
}

func (r *Reporter) send(ctx context.Context, payload models.AgentMetricsPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/agent/metrics", r.backendURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("X-Agent-ID", r.agentID)

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
