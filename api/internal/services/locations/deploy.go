package locations

import (
	"context"
	"fmt"

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
		natsURL = "nats://<your-nats-host>:4222"
	}

	env := map[string]string{
		"NATS_URL":           natsURL,
		"WORKER_LOCATION_ID": location.ID.String(),
		"HTTP_PORT":          "8080",
		"METRICS_PORT":       "9090",
	}

	containerName := "probara-worker-" + location.Slug
	dockerRun := fmt.Sprintf(`docker run -d \
  --name %s \
  --restart unless-stopped \
  -e NATS_URL=%q \
  -e WORKER_LOCATION_ID=%q \
  -e HTTP_PORT=8080 \
  -e METRICS_PORT=9090 \
  %s`, containerName, natsURL, location.ID.String(), workerImage)

	compose := fmt.Sprintf(`services:
  worker-%s:
    image: %s
    restart: unless-stopped
    environment:
      NATS_URL: %q
      WORKER_LOCATION_ID: %q
      HTTP_PORT: "8080"
      METRICS_PORT: "9090"
`, location.Slug, workerImage, natsURL, location.ID.String())

	return &models.LocationDeployInfo{
		LocationID:        location.ID,
		NATSURL:           natsURL,
		DockerRunCommand:  dockerRun,
		DockerComposeYAML: compose,
		Env:               env,
	}, nil
}
