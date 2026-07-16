package locations

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// workerImage is the published worker image the deploy snippets reference
// (matches the Helm chart's "{repository}-worker:{tag}" convention).
const workerImage = "ghcr.io/yassinebenameur/probara-worker:latest"

// GenerateDeployInfo builds ready-to-paste deployment snippets for a
// location's worker. The worker only needs NATS reachability — results travel
// back over NATS and are persisted platform-side, so no database URL appears
// anywhere here.
func (s *Service) GenerateDeployInfo(ctx context.Context, tenantID, locationID uuid.UUID, publicNATSURL string) (*models.LocationDeployInfo, error) {
	location, err := s.Get(ctx, tenantID, locationID)
	if err != nil {
		return nil, err
	}
	natsURL := publicNATSURL
	if natsURL == "" {
		return nil, errors.New("PUBLIC_NATS_URL is not configured; private worker deployment is disabled")
	}
	credentialURL, err := locationWorkerNATSURL(natsURL, location.ID.String(), "validate-only")
	if err != nil {
		return nil, err
	}
	_ = credentialURL
	credential, err := s.workerCredential(ctx, tenantID, locationID)
	if err != nil {
		return nil, err
	}
	natsURL, err = locationWorkerNATSURL(natsURL, location.ID.String(), credential)
	if err != nil {
		return nil, err
	}

	env := map[string]string{
		"NATS_URL":            natsURL,
		"WORKER_LOCATION_ID":  location.ID.String(),
		"LOCATION_CREDENTIAL": credential,
		"HTTP_PORT":           "8080",
		"METRICS_PORT":        "9090",
	}

	containerName := "probara-worker-" + location.Slug
	// Port 8080 is the mesh echo endpoint (GET /mesh/echo). Publish it and set
	// this host's reachable host:port as the location's "Mesh endpoint" to let
	// other locations probe network connectivity to this one.
	dockerRun := fmt.Sprintf(`docker run -d \
  --name %s \
  --restart unless-stopped \
  -p 8080:8080 \
  -e NATS_URL=%q \
  -e WORKER_LOCATION_ID=%q \
  -e LOCATION_CREDENTIAL=%q \
  -e HTTP_PORT=8080 \
  -e METRICS_PORT=9090 \
  %s`, containerName, natsURL, location.ID.String(), credential, workerImage)

	compose := fmt.Sprintf(`services:
  worker-%s:
    image: %s
    restart: unless-stopped
    ports:
      # Mesh echo port — set this host's host:port as the location's Mesh endpoint.
      - "8080:8080"
    environment:
      NATS_URL: %q
      WORKER_LOCATION_ID: %q
      LOCATION_CREDENTIAL: %q
      HTTP_PORT: "8080"
      METRICS_PORT: "9090"
`, location.Slug, workerImage, natsURL, location.ID.String(), credential)

	return &models.LocationDeployInfo{
		LocationID:        location.ID,
		NATSURL:           natsURL,
		DockerRunCommand:  dockerRun,
		DockerComposeYAML: compose,
		Env:               env,
	}, nil
}

func locationWorkerNATSURL(baseURL, locationID, credential string) (string, error) {
	parsedNATSURL, err := url.Parse(baseURL)
	if err != nil || parsedNATSURL.Scheme == "" || parsedNATSURL.Host == "" {
		return "", fmt.Errorf("invalid public NATS URL %q", baseURL)
	}
	if parsedNATSURL.User != nil {
		return "", fmt.Errorf("public NATS URL must not contain userinfo")
	}
	if parsedNATSURL.Scheme != "tls" && parsedNATSURL.Scheme != "wss" {
		return "", fmt.Errorf("public NATS URL must use tls:// or wss://")
	}
	parsedNATSURL.User = url.UserPassword(locationID, credential)
	return parsedNATSURL.String(), nil
}
