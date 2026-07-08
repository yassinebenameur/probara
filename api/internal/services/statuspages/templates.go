package statuspages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/statustemplate"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

// ErrInvalidTemplate marks template-source validation failures so handlers
// can map them to 400 instead of 500. The wrapped message carries the parse
// error (with line number) for the editor.
var ErrInvalidTemplate = errors.New("invalid template")

func (s *Service) ensureStatusPage(ctx context.Context, q statusPageDBTX, tenantID, pageID uuid.UUID) error {
	var one int
	if err := q.QueryRowContext(ctx, `SELECT 1 FROM status_pages WHERE id = $1 AND tenant_id = $2`, pageID, tenantID).Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("status page not found")
		}
		return fmt.Errorf("failed to check status page: %w", err)
	}
	return nil
}

// GetTemplateState returns the full custom-template state for a page: draft
// source, published version, and version history (newest first).
func (s *Service) GetTemplateState(ctx context.Context, tenantID, pageID uuid.UUID) (*models.StatusPageTemplateState, error) {
	if err := s.ensureStatusPage(ctx, s.db, tenantID, pageID); err != nil {
		return nil, err
	}

	state := &models.StatusPageTemplateState{
		Versions:     make([]models.StatusPageTemplateVersion, 0),
		MaxSizeBytes: statustemplate.MaxSourceSize,
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT version, status, octet_length(source), created_at, updated_at, published_at
		FROM status_page_templates
		WHERE status_page_id = $1
		ORDER BY status = 'draft' DESC, version DESC NULLS LAST
	`, pageID)
	if err != nil {
		return nil, fmt.Errorf("failed to list template versions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var v models.StatusPageTemplateVersion
		var version sql.NullInt64
		if err := rows.Scan(&version, &v.Status, &v.SizeBytes, &v.CreatedAt, &v.UpdatedAt, &v.PublishedAt); err != nil {
			return nil, fmt.Errorf("failed to scan template version: %w", err)
		}
		if version.Valid {
			value := int(version.Int64)
			v.Version = &value
		}
		if v.Status == "published" {
			state.HasCustom = true
			state.PublishedVersion = v.Version
		}
		if v.Status == "draft" {
			updatedAt := v.UpdatedAt
			state.DraftUpdatedAt = &updatedAt
		}
		state.Versions = append(state.Versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating template versions: %w", err)
	}

	var draftSource string
	err = s.db.QueryRowContext(ctx, `
		SELECT source FROM status_page_templates
		WHERE status_page_id = $1 AND status = 'draft'
	`, pageID).Scan(&draftSource)
	switch {
	case err == nil:
		state.DraftSource = &draftSource
	case err != sql.ErrNoRows:
		return nil, fmt.Errorf("failed to get draft source: %w", err)
	}

	return state, nil
}

// GetTemplateVersionSource returns the stored source of one template version.
func (s *Service) GetTemplateVersionSource(ctx context.Context, tenantID, pageID uuid.UUID, version int) (string, error) {
	if err := s.ensureStatusPage(ctx, s.db, tenantID, pageID); err != nil {
		return "", err
	}
	var source string
	err := s.db.QueryRowContext(ctx, `
		SELECT source FROM status_page_templates
		WHERE status_page_id = $1 AND version = $2
	`, pageID, version).Scan(&source)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("template version not found")
		}
		return "", fmt.Errorf("failed to get template version: %w", err)
	}
	return source, nil
}

// SaveTemplateDraft validates source and upserts the page's single draft row.
func (s *Service) SaveTemplateDraft(ctx context.Context, tenantID, pageID uuid.UUID, source string) error {
	if err := statustemplate.Validate(source); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTemplate, err)
	}
	if err := s.ensureStatusPage(ctx, s.db, tenantID, pageID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO status_page_templates (id, tenant_id, status_page_id, status, source)
		VALUES ($1, $2, $3, 'draft', $4)
		ON CONFLICT (status_page_id) WHERE status = 'draft'
		DO UPDATE SET source = EXCLUDED.source, updated_at = NOW()
	`, uuid.New(), tenantID, pageID, source)
	if err != nil {
		return fmt.Errorf("failed to save template draft: %w", err)
	}
	return nil
}

// DiscardTemplateDraft deletes the page's draft, if any (idempotent).
func (s *Service) DiscardTemplateDraft(ctx context.Context, tenantID, pageID uuid.UUID) error {
	if err := s.ensureStatusPage(ctx, s.db, tenantID, pageID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM status_page_templates
		WHERE status_page_id = $1 AND status = 'draft'
	`, pageID); err != nil {
		return fmt.Errorf("failed to discard template draft: %w", err)
	}
	return nil
}

// PublishTemplateDraft promotes the draft to the published template: the
// previous published version (if any) is archived and the draft receives the
// next version number. The public renderer picks the new version up via the
// NATS invalidation event (or, without NATS, within the render-cache TTL).
func (s *Service) PublishTemplateDraft(ctx context.Context, tenantID, pageID uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := s.ensureStatusPage(ctx, tx, tenantID, pageID); err != nil {
		return err
	}

	var draftID uuid.UUID
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM status_page_templates
		WHERE status_page_id = $1 AND status = 'draft'
		FOR UPDATE
	`, pageID).Scan(&draftID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("no draft template to publish")
		}
		return fmt.Errorf("failed to get draft template: %w", err)
	}

	if err := s.publishRow(ctx, tx, pageID, draftID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	s.notifyStatusPage(tenantID, pageID, "template_published")
	return nil
}

// RevertTemplate republishes an archived version's source as a fresh version
// (history stays append-only, so a revert can itself be reverted).
func (s *Service) RevertTemplate(ctx context.Context, tenantID, pageID uuid.UUID, version int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := s.ensureStatusPage(ctx, tx, tenantID, pageID); err != nil {
		return err
	}

	var source, status string
	err = tx.QueryRowContext(ctx, `
		SELECT source, status FROM status_page_templates
		WHERE status_page_id = $1 AND version = $2
		FOR UPDATE
	`, pageID, version).Scan(&source, &status)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("template version not found")
		}
		return fmt.Errorf("failed to get template version: %w", err)
	}
	if status == "published" {
		return fmt.Errorf("version %d is already published", version)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE status_page_templates
		SET status = 'archived', updated_at = NOW()
		WHERE status_page_id = $1 AND status = 'published'
	`, pageID); err != nil {
		return fmt.Errorf("failed to archive published template: %w", err)
	}

	var nextVersion int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1 FROM status_page_templates
		WHERE status_page_id = $1
	`, pageID).Scan(&nextVersion); err != nil {
		return fmt.Errorf("failed to compute next template version: %w", err)
	}

	// Insert the copy directly as the published row: an intermediate
	// versionless non-draft state would trip the table's check constraint.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO status_page_templates (id, tenant_id, status_page_id, version, status, source, published_at)
		VALUES ($1, $2, $3, $4, 'published', $5, NOW())
	`, uuid.New(), tenantID, pageID, nextVersion, source); err != nil {
		return fmt.Errorf("failed to copy template version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	s.notifyStatusPage(tenantID, pageID, "template_published")
	return nil
}

// ResetTemplate archives the published custom template so the page renders
// with the built-in template again (idempotent).
func (s *Service) ResetTemplate(ctx context.Context, tenantID, pageID uuid.UUID) error {
	if err := s.ensureStatusPage(ctx, s.db, tenantID, pageID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE status_page_templates
		SET status = 'archived', updated_at = NOW()
		WHERE status_page_id = $1 AND status = 'published'
	`, pageID); err != nil {
		return fmt.Errorf("failed to reset template: %w", err)
	}
	s.notifyStatusPage(tenantID, pageID, "template_reset")
	return nil
}

// publishRow archives the currently published row and marks rowID published
// with the next version number. Must run inside a transaction.
func (s *Service) publishRow(ctx context.Context, tx statusPageDBTX, pageID, rowID uuid.UUID) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE status_page_templates
		SET status = 'archived', updated_at = NOW()
		WHERE status_page_id = $1 AND status = 'published'
	`, pageID); err != nil {
		return fmt.Errorf("failed to archive published template: %w", err)
	}

	var nextVersion int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1 FROM status_page_templates
		WHERE status_page_id = $1
	`, pageID).Scan(&nextVersion); err != nil {
		return fmt.Errorf("failed to compute next template version: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE status_page_templates
		SET status = 'published', version = $2, published_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, rowID, nextVersion); err != nil {
		return fmt.Errorf("failed to publish template: %w", err)
	}
	return nil
}

// notifyStatusPage fires a NATS status-update event so the public renderer
// invalidates its per-slug HTML cache immediately. Best-effort: without NATS
// the render-cache TTL bounds staleness.
func (s *Service) notifyStatusPage(tenantID, pageID uuid.UUID, eventType string) {
	if s.publisher == nil {
		return
	}
	_ = s.publisher.Publish(statusupdates.Event{
		Type:         eventType,
		StatusPageID: pageID.String(),
		TenantID:     tenantID.String(),
		Timestamp:    time.Now().UTC(),
	})
}
