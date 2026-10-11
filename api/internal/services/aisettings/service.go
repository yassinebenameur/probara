// Package aisettings manages workspace enablement for bundled local AI.
package aisettings

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/ai"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"time"
)

type Settings struct {
	Enabled             bool   `json:"enabled"`
	InstallationEnabled bool   `json:"installation_enabled"`
	Available           bool   `json:"available"`
	Model               string `json:"model,omitempty"`
	Message             string `json:"message,omitempty"`
}
type UpdateRequest struct {
	Enabled *bool `json:"enabled"`
}
type TestRequest struct{}
type TestResult struct {
	OK      bool   `json:"ok"`
	Model   string `json:"model,omitempty"`
	Message string `json:"message,omitempty"`
}
type Service struct {
	db     *shareddb.Client
	config ai.Config
}

func NewService(db *shareddb.Client, config ai.Config) *Service {
	return &Service{db: db, config: config}
}
func (s *Service) EffectiveEnabled(ctx context.Context, id uuid.UUID) (bool, error) {
	return ai.EffectiveEnabled(ctx, s.db, id, s.config.Disabled)
}
func (s *Service) EffectiveConfig(ctx context.Context, id uuid.UUID) (ai.Config, bool, error) {
	enabled, err := s.EffectiveEnabled(ctx, id)
	return s.config, enabled, err
}
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Settings, error) {
	enabled, err := s.EffectiveEnabled(ctx, id)
	if err != nil {
		return nil, err
	}
	settings := &Settings{Enabled: enabled, InstallationEnabled: !s.config.Disabled}
	if s.config.Disabled {
		settings.Message = "Local AI is disabled for this installation"
		return settings, nil
	}
	probe := s.Test(ctx, id, TestRequest{})
	settings.Available = probe.OK
	settings.Model = probe.Model
	settings.Message = probe.Message
	return settings, nil
}
func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateRequest) (*Settings, error) {
	if req.Enabled == nil {
		return nil, fmt.Errorf("enabled is required; external provider fields are retired")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO ai_settings (tenant_id,enabled) VALUES ($1,$2)
 ON CONFLICT (tenant_id) DO UPDATE SET enabled=EXCLUDED.enabled,updated_at=NOW()`, id, *req.Enabled)
	if err != nil {
		return nil, fmt.Errorf("update workspace AI enablement: %w", err)
	}
	return s.Get(ctx, id)
}
func (s *Service) Test(ctx context.Context, id uuid.UUID, _ TestRequest) TestResult {
	if s.config.Disabled {
		return TestResult{Message: "Local AI is disabled for this installation"}
	}
	// Test the installation's service regardless of the workspace enable switch.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	model, err := ai.Probe(ctx, s.config)
	if err != nil {
		return TestResult{Message: err.Error()}
	}
	return TestResult{OK: true, Model: model}
}
