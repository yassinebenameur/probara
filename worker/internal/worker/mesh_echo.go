package worker

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
)

// MeshEchoHandler serves GET /mesh/echo: the target side of an inter-location
// mesh probe. The response identifies which location answered so the probing
// worker can detect a misrouted or stale mesh_endpoint. locationID is empty on
// default-fleet workers — probes against those fail the identity assertion,
// which is intended (the default fleet is not a mesh node).
func MeshEchoHandler(locationID string) http.HandlerFunc {
	hostname, _ := os.Hostname()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(models.MeshEchoResponse{
			LocationID: locationID,
			Hostname:   hostname,
			Timestamp:  time.Now().UTC(),
		})
	}
}
