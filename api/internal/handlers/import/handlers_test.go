package importhandlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"

	"github.com/yassinebenameur/probara/api/internal/models"
	importservice "github.com/yassinebenameur/probara/api/internal/services/import"
)

type importServiceMock struct {
	exportData []byte
}

func (m *importServiceMock) ParseFile(data []byte, filename string) (*models.ImportPreviewResponse, error) {
	return &models.ImportPreviewResponse{}, nil
}

func (m *importServiceMock) ExecuteImport(ctx context.Context, tenantID uuid.UUID, req *models.ImportExecuteRequest) (*models.ImportExecuteResponse, error) {
	return &models.ImportExecuteResponse{}, nil
}

func (m *importServiceMock) ExportMonitors(ctx context.Context, tenantID uuid.UUID) ([]byte, error) {
	return m.exportData, nil
}

func TestHandlers_Export(t *testing.T) {
	log := logger.New("test", "debug")
	service := &importServiceMock{exportData: []byte("kind: monitor_export\nversion: 1\nmonitors: []\n")}
	handlers := NewHandlers(service, log)
	tenantID := uuid.New()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors/export", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID.String()))

	w := httptest.NewRecorder()
	handlers.Export(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/x-yaml" {
		t.Fatalf("content-type = %q, want application/x-yaml", got)
	}
	if got := w.Header().Get("Content-Disposition"); got == "" {
		t.Fatal("expected content-disposition header")
	}
	if body := w.Body.String(); body != string(service.exportData) {
		t.Fatalf("body = %q, want %q", body, string(service.exportData))
	}
}

func TestHandlers_PreviewPortableExportYAML(t *testing.T) {
	log := logger.New("test", "debug")
	service := importservice.NewService(nil, nil)
	handlers := NewHandlers(service, log)
	tenantID := uuid.New()

	payload := []byte(`
kind: monitor_export
version: 1
monitors:
  - name: API
    type: http
    interval_seconds: 60
    timeout_seconds: 30
    enabled: true
    config:
      url: https://example.com/health
      method: GET
`)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "monitors.yaml")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/import/preview", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID.String()))

	w := httptest.NewRecorder()
	handlers.Preview(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var preview models.ImportPreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatalf("failed to decode preview: %v", err)
	}
	if preview.Schema != models.ImportSchemaPortableMonitorExport {
		t.Fatalf("schema = %q, want %q", preview.Schema, models.ImportSchemaPortableMonitorExport)
	}
	if preview.SuggestedMapping.Config != "config" {
		t.Fatalf("config mapping = %q, want config", preview.SuggestedMapping.Config)
	}
}
