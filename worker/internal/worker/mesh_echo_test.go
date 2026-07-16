package worker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/models"
)

func TestMeshEchoHandler_GetReturnsIdentity(t *testing.T) {
	locationID := uuid.New().String()
	srv := httptest.NewServer(MeshEchoHandler(locationID))
	defer srv.Close()

	resp, err := http.Get(srv.URL + models.MeshEchoPath)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var echo models.MeshEchoResponse
	if err := json.NewDecoder(resp.Body).Decode(&echo); err != nil {
		t.Fatalf("decode echo response: %v", err)
	}
	if echo.LocationID != locationID {
		t.Errorf("location_id = %q, want %q", echo.LocationID, locationID)
	}
	if echo.Hostname == "" {
		t.Error("expected hostname to be populated")
	}
	if echo.Timestamp.IsZero() {
		t.Error("expected timestamp to be populated")
	}
}

func TestMeshEchoHandler_NonGetIsMethodNotAllowed(t *testing.T) {
	srv := httptest.NewServer(MeshEchoHandler(uuid.New().String()))
	defer srv.Close()

	resp, err := http.Post(srv.URL+models.MeshEchoPath, "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}
