package locations

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/buildinfo"
)

// workerImageRepository is the published worker image (the Helm chart's
// "{repository}-worker" convention, at its default repository).
const workerImageRepository = "ghcr.io/yassinebenameur/probara-worker"

// DefaultWorkerImage is the image deploy snippets reference when
// LOCATION_WORKER_IMAGE is unset: the worker at this API's own release
// version, so a location worker speaks the same NATS contract as the
// platform it joins. Every release, prerelease included, pushes that exact
// tag. Unreleased builds (dev, branch builds) have no matching tag, so they
// fall back to "alpha", the newest prerelease. Never "latest": it moves only
// on stable releases and lagged the platform by a major version.
func DefaultWorkerImage() string {
	if v, ok := buildinfo.ReleaseVersion(); ok {
		return workerImageRepository + ":" + v
	}
	return workerImageRepository + ":alpha"
}

// ConfigureWorkerImage sets the image deploy snippets reference
// (LOCATION_WORKER_IMAGE). Empty keeps DefaultWorkerImage.
func (s *Service) ConfigureWorkerImage(image string) {
	s.workerImage = strings.TrimSpace(image)
}

func (s *Service) deployWorkerImage() string {
	if s.workerImage != "" {
		return s.workerImage
	}
	return DefaultWorkerImage()
}

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

	image := s.deployWorkerImage()
	containerName := "probara-worker-" + location.Slug
	// Port 8080 is the mesh echo endpoint (GET /mesh/echo). Publish it and set
	// this host's reachable host:port as the location's "Mesh endpoint" to let
	// other locations probe network connectivity to this one.
	dockerRun := fmt.Sprintf(`# %s
docker run -d \
  --name %s \
  --restart unless-stopped \
  -p 8080:8080 \
  -e NATS_URL=%q \
  -e WORKER_LOCATION_ID=%q \
  -e LOCATION_CREDENTIAL=%q \
  -e HTTP_PORT=8080 \
  -e METRICS_PORT=9090 \
  %s`, natsTransportNote(natsURL), containerName, natsURL, location.ID.String(), credential, image)

	compose := fmt.Sprintf(`services:
  worker-%s:
    image: %s
    restart: unless-stopped
    ports:
      # Mesh echo port — set this host's host:port as the location's Mesh endpoint.
      - "8080:8080"
    environment:
      # %s
      NATS_URL: %q
      WORKER_LOCATION_ID: %q
      LOCATION_CREDENTIAL: %q
      HTTP_PORT: "8080"
      METRICS_PORT: "9090"
`, location.Slug, image, natsTransportNote(natsURL), natsURL, location.ID.String(), credential)

	return &models.LocationDeployInfo{
		LocationID:        location.ID,
		NATSURL:           natsURL,
		DockerRunCommand:  dockerRun,
		DockerComposeYAML: compose,
		Env:               env,
	}, nil
}

// natsTransportNote is the one-line comment the snippets carry above the
// broker URL. A wss:// PUBLIC_NATS_URL (the broker's websocket listener behind
// an HTTPS ingress) needs only outbound HTTPS from the location's network,
// which is what restricted networks usually allow; tls:// needs the broker's
// TCP port opened outbound, so that note points at the wss alternative.
func natsTransportNote(natsURL string) string {
	if strings.HasPrefix(strings.ToLower(natsURL), "wss://") {
		return "NATS over WebSocket (wss://): this host needs only outbound HTTPS to the broker."
	}
	return "NATS over TLS (tls://): this host needs outbound access to the broker's TCP port. A wss:// PUBLIC_NATS_URL needs only outbound HTTPS."
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
		return "", fmt.Errorf("public NATS URL must use tls:// or wss:// (wss:// is the broker's websocket listener behind an HTTPS ingress, reachable over 443)")
	}
	parsedNATSURL.User = url.UserPassword(locationID, credential)
	return parsedNATSURL.String(), nil
}
