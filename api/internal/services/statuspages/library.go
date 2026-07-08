package statuspages

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/statustemplate"
)

const maxLibraryTemplateNameLen = 100

func validateLibraryTemplateName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("template name is required")
	}
	if len(trimmed) > maxLibraryTemplateNameLen {
		return "", fmt.Errorf("template name must be %d characters or less", maxLibraryTemplateNameLen)
	}
	return trimmed, nil
}

// ListLibraryTemplates returns the tenant's library templates (metadata only,
// no source — entries are ~100KB each).
func (s *Service) ListLibraryTemplates(ctx context.Context, tenantID uuid.UUID) (*models.StatusPageLibraryTemplateListResponse, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, octet_length(source), created_at, updated_at
		FROM status_page_template_library
		WHERE tenant_id = $1
		ORDER BY lower(name) ASC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list library templates: %w", err)
	}
	defer rows.Close()

	items := make([]models.StatusPageLibraryTemplate, 0)
	for rows.Next() {
		var item models.StatusPageLibraryTemplate
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.SizeBytes, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan library template: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating library templates: %w", err)
	}
	return &models.StatusPageLibraryTemplateListResponse{Items: items, Total: len(items)}, nil
}

// GetLibraryTemplate returns one library template including its source.
func (s *Service) GetLibraryTemplate(ctx context.Context, tenantID, templateID uuid.UUID) (*models.StatusPageLibraryTemplate, error) {
	var item models.StatusPageLibraryTemplate
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, source, octet_length(source), created_at, updated_at
		FROM status_page_template_library
		WHERE id = $1 AND tenant_id = $2
	`, templateID, tenantID).Scan(
		&item.ID, &item.Name, &item.Description, &item.Source, &item.SizeBytes, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("library template not found")
		}
		return nil, fmt.Errorf("failed to get library template: %w", err)
	}
	return &item, nil
}

// CreateLibraryTemplate validates and stores a new library template.
func (s *Service) CreateLibraryTemplate(ctx context.Context, tenantID uuid.UUID, req *models.CreateStatusPageLibraryTemplateRequest) (*models.StatusPageLibraryTemplate, error) {
	name, err := validateLibraryTemplateName(req.Name)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTemplate, err)
	}
	if err := statustemplate.Validate(req.Source); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTemplate, err)
	}

	id := uuid.New()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO status_page_template_library (id, tenant_id, name, description, source)
		VALUES ($1, $2, $3, $4, $5)
	`, id, tenantID, name, trimOptionalString(req.Description), req.Source); err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return nil, fmt.Errorf("template name already exists")
		}
		return nil, fmt.Errorf("failed to create library template: %w", err)
	}
	return s.GetLibraryTemplate(ctx, tenantID, id)
}

// UpdateLibraryTemplate partially updates a library template.
func (s *Service) UpdateLibraryTemplate(ctx context.Context, tenantID, templateID uuid.UUID, req *models.UpdateStatusPageLibraryTemplateRequest) (*models.StatusPageLibraryTemplate, error) {
	setParts := make([]string, 0)
	args := make([]interface{}, 0)
	argIndex := 1

	if req.Name != nil {
		name, err := validateLibraryTemplateName(*req.Name)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidTemplate, err)
		}
		setParts = append(setParts, fmt.Sprintf("name = $%d", argIndex))
		args = append(args, name)
		argIndex++
	}
	if req.Description != nil {
		setParts = append(setParts, fmt.Sprintf("description = $%d", argIndex))
		args = append(args, trimOptionalString(req.Description))
		argIndex++
	}
	if req.Source != nil {
		if err := statustemplate.Validate(*req.Source); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidTemplate, err)
		}
		setParts = append(setParts, fmt.Sprintf("source = $%d", argIndex))
		args = append(args, *req.Source)
		argIndex++
	}

	if len(setParts) > 0 {
		setParts = append(setParts, "updated_at = NOW()")
		args = append(args, templateID, tenantID)
		query := fmt.Sprintf(`
			UPDATE status_page_template_library
			SET %s
			WHERE id = $%d AND tenant_id = $%d
		`, strings.Join(setParts, ", "), argIndex, argIndex+1)
		result, err := s.db.ExecContext(ctx, query, args...)
		if err != nil {
			if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
				return nil, fmt.Errorf("template name already exists")
			}
			return nil, fmt.Errorf("failed to update library template: %w", err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("failed to get rows affected: %w", err)
		}
		if rowsAffected == 0 {
			return nil, fmt.Errorf("library template not found")
		}
	}

	return s.GetLibraryTemplate(ctx, tenantID, templateID)
}

// DeleteLibraryTemplate removes a library template. Pages that applied it
// keep their copies — deleting a library entry never touches a live page.
func (s *Service) DeleteLibraryTemplate(ctx context.Context, tenantID, templateID uuid.UUID) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM status_page_template_library
		WHERE id = $1 AND tenant_id = $2
	`, templateID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to delete library template: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("library template not found")
	}
	return nil
}
