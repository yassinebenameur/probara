package statuspages

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
)

type statusPageSettingsStored struct {
	ShowMonitorTags   bool    `json:"show_monitor_tags"`
	ShowMonitorURL    bool    `json:"show_monitor_url"`
	ShowMonitorUptime bool    `json:"show_monitor_uptime"`
	ShowMonitorTLS    bool    `json:"show_monitor_tls"`
	ShowLatencyCharts bool    `json:"show_latency_charts"`
	ShowAgentMetrics  bool    `json:"show_agent_metrics"`
	ShowGlobalUptime  bool    `json:"show_global_uptime"`
	ShowFooter        bool    `json:"show_footer"`
	FooterText        *string `json:"footer_text,omitempty"`
	DefaultTheme      string  `json:"default_theme"`
	AllowThemeToggle  bool    `json:"allow_theme_toggle"`
}

func defaultStatusPageSettings() statusPageSettingsStored {
	return statusPageSettingsStored{
		ShowMonitorTags:   true,
		ShowMonitorURL:    true,
		ShowMonitorUptime: true,
		ShowMonitorTLS:    true,
		ShowLatencyCharts: true,
		ShowAgentMetrics:  true,
		ShowGlobalUptime:  true,
		ShowFooter:        true,
		DefaultTheme:      "dark",
		AllowThemeToggle:  true,
	}
}

func (s statusPageSettingsStored) applyPatch(patch *models.StatusPageSettings) statusPageSettingsStored {
	if patch == nil {
		return s
	}
	if patch.ShowMonitorTags != nil {
		s.ShowMonitorTags = *patch.ShowMonitorTags
	}
	if patch.ShowMonitorURL != nil {
		s.ShowMonitorURL = *patch.ShowMonitorURL
	}
	if patch.ShowMonitorUptime != nil {
		s.ShowMonitorUptime = *patch.ShowMonitorUptime
	}
	if patch.ShowMonitorTLS != nil {
		s.ShowMonitorTLS = *patch.ShowMonitorTLS
	}
	if patch.ShowLatencyCharts != nil {
		s.ShowLatencyCharts = *patch.ShowLatencyCharts
	}
	if patch.ShowAgentMetrics != nil {
		s.ShowAgentMetrics = *patch.ShowAgentMetrics
	}
	if patch.ShowGlobalUptime != nil {
		s.ShowGlobalUptime = *patch.ShowGlobalUptime
	}
	if patch.ShowFooter != nil {
		s.ShowFooter = *patch.ShowFooter
	}
	if patch.FooterText != nil {
		v := strings.TrimSpace(*patch.FooterText)
		if v == "" {
			s.FooterText = nil
		} else {
			s.FooterText = &v
		}
	}
	if patch.DefaultTheme != nil {
		switch strings.ToLower(strings.TrimSpace(*patch.DefaultTheme)) {
		case "light":
			s.DefaultTheme = "light"
		default:
			s.DefaultTheme = "dark"
		}
	}
	if patch.AllowThemeToggle != nil {
		s.AllowThemeToggle = *patch.AllowThemeToggle
	}
	return s
}

func parseStatusPageSettings(settingsJSON []byte) statusPageSettingsStored {
	stored := defaultStatusPageSettings()
	if len(settingsJSON) == 0 {
		return stored
	}
	var patch models.StatusPageSettings
	if err := json.Unmarshal(settingsJSON, &patch); err != nil {
		return stored
	}
	return stored.applyPatch(&patch)
}

func (s statusPageSettingsStored) toAPI() *models.StatusPageSettings {
	showMonitorTags := s.ShowMonitorTags
	showMonitorURL := s.ShowMonitorURL
	showMonitorUptime := s.ShowMonitorUptime
	showMonitorTLS := s.ShowMonitorTLS
	showLatencyCharts := s.ShowLatencyCharts
	showAgentMetrics := s.ShowAgentMetrics
	showGlobalUptime := s.ShowGlobalUptime
	showFooter := s.ShowFooter
	defaultTheme := s.DefaultTheme
	allowThemeToggle := s.AllowThemeToggle
	return &models.StatusPageSettings{
		ShowMonitorTags:   &showMonitorTags,
		ShowMonitorURL:    &showMonitorURL,
		ShowMonitorUptime: &showMonitorUptime,
		ShowMonitorTLS:    &showMonitorTLS,
		ShowLatencyCharts: &showLatencyCharts,
		ShowAgentMetrics:  &showAgentMetrics,
		ShowGlobalUptime:  &showGlobalUptime,
		ShowFooter:        &showFooter,
		FooterText:        s.FooterText,
		DefaultTheme:      &defaultTheme,
		AllowThemeToggle:  &allowThemeToggle,
	}
}

// Service handles status page business logic
type Service struct {
	db *db.Client
}

// NewService creates a new status page service
func NewService(db *db.Client) *Service {
	return &Service{db: db}
}

// CreateStatusPage creates a new status page
func (s *Service) CreateStatusPage(ctx context.Context, tenantID uuid.UUID, req *models.CreateStatusPageRequest) (*models.StatusPage, error) {
	pageID := uuid.New()

	// Start transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert status page
	query := `
		INSERT INTO status_pages (
			id, tenant_id, slug, title, description, logo_url,
			primary_color, secondary_color, settings, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		RETURNING id, tenant_id, slug, title, description, logo_url,
			primary_color, secondary_color, settings, created_at, updated_at
	`

	var page models.StatusPage
	settingsStored := defaultStatusPageSettings().applyPatch(req.Settings)
	settingsJSON, _ := json.Marshal(settingsStored)
	var settingsOut []byte
	err = tx.QueryRowContext(ctx, query,
		pageID, tenantID, req.Slug, req.Title, req.Description,
		req.LogoURL, req.PrimaryColor, req.SecondaryColor, settingsJSON,
	).Scan(
		&page.ID, &page.TenantID, &page.Slug, &page.Title,
		&page.Description, &page.LogoURL, &page.PrimaryColor,
		&page.SecondaryColor, &settingsOut, &page.CreatedAt, &page.UpdatedAt,
	)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return nil, fmt.Errorf("slug already exists")
		}
		return nil, fmt.Errorf("failed to create status page: %w", err)
	}

	page.Settings = parseStatusPageSettings(settingsOut).toAPI()

	// Validate and insert monitor associations
	if len(req.MonitorIDs) > 0 {
		monitorUUIDs, err := s.validateAndParseMonitorIDs(ctx, tx, tenantID, req.MonitorIDs)
		if err != nil {
			return nil, err
		}

		if len(monitorUUIDs) > 0 {
			insertQuery := `
				INSERT INTO status_page_monitors (status_page_id, monitor_id, position, display_name)
				VALUES ($1, $2, $3, $4)
			`
			for pos, monitorID := range monitorUUIDs {
				var displayName *string
				if raw, ok := req.MonitorDisplayNames[monitorID.String()]; ok {
					v := strings.TrimSpace(raw)
					if v != "" {
						displayName = &v
					}
				}
				_, err := tx.ExecContext(ctx, insertQuery, pageID, monitorID, pos, displayName)
				if err != nil {
					if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
						// Duplicate key, skip
						continue
					}
					return nil, fmt.Errorf("failed to associate monitor: %w", err)
				}
			}
			page.MonitorIDs = monitorUUIDs
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &page, nil
}

// GetStatusPage retrieves a status page by ID (tenant-scoped)
func (s *Service) GetStatusPage(ctx context.Context, tenantID, pageID uuid.UUID) (*models.StatusPage, error) {
	query := `
		SELECT id, tenant_id, slug, title, description, logo_url,
			primary_color, secondary_color, settings, created_at, updated_at
		FROM status_pages
		WHERE id = $1 AND tenant_id = $2
	`

	var page models.StatusPage
	var settingsJSON []byte
	err := s.db.QueryRowContext(ctx, query, pageID, tenantID).Scan(
		&page.ID, &page.TenantID, &page.Slug, &page.Title,
		&page.Description, &page.LogoURL, &page.PrimaryColor,
		&page.SecondaryColor, &settingsJSON, &page.CreatedAt, &page.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("status page not found")
		}
		return nil, fmt.Errorf("failed to get status page: %w", err)
	}
	page.Settings = parseStatusPageSettings(settingsJSON).toAPI()

	// Get associated monitor IDs
	monitorQuery := `
		SELECT monitor_id
		FROM status_page_monitors
		WHERE status_page_id = $1
		ORDER BY position ASC
	`
	rows, err := s.db.QueryContext(ctx, monitorQuery, pageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get monitor IDs: %w", err)
	}
	defer rows.Close()

	var monitorIDs []uuid.UUID
	for rows.Next() {
		var monitorID uuid.UUID
		if err := rows.Scan(&monitorID); err != nil {
			return nil, fmt.Errorf("failed to scan monitor ID: %w", err)
		}
		monitorIDs = append(monitorIDs, monitorID)
	}
	page.MonitorIDs = monitorIDs

	return &page, nil
}

// ListStatusPages lists status pages with pagination
func (s *Service) ListStatusPages(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.StatusPageListResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := (page - 1) * pageSize

	// Count total
	countQuery := `SELECT COUNT(*) FROM status_pages WHERE tenant_id = $1`
	var total int
	err := s.db.QueryRowContext(ctx, countQuery, tenantID).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("failed to count status pages: %w", err)
	}

	// Get pages
	query := `
		SELECT id, tenant_id, slug, title, description, logo_url,
			primary_color, secondary_color, settings, created_at, updated_at
		FROM status_pages
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list status pages: %w", err)
	}
	defer rows.Close()

	var pages []models.StatusPage
	for rows.Next() {
		var page models.StatusPage
		var settingsJSON []byte
		err := rows.Scan(
			&page.ID, &page.TenantID, &page.Slug, &page.Title,
			&page.Description, &page.LogoURL, &page.PrimaryColor,
			&page.SecondaryColor, &settingsJSON, &page.CreatedAt, &page.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan status page: %w", err)
		}
		page.Settings = parseStatusPageSettings(settingsJSON).toAPI()

		// Get associated monitor IDs
		monitorQuery := `
			SELECT monitor_id
			FROM status_page_monitors
			WHERE status_page_id = $1
			ORDER BY position ASC
		`
		monitorRows, err := s.db.QueryContext(ctx, monitorQuery, page.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get monitor IDs: %w", err)
		}

		var monitorIDs []uuid.UUID
		for monitorRows.Next() {
			var monitorID uuid.UUID
			if err := monitorRows.Scan(&monitorID); err != nil {
				monitorRows.Close()
				return nil, fmt.Errorf("failed to scan monitor ID: %w", err)
			}
			monitorIDs = append(monitorIDs, monitorID)
		}
		monitorRows.Close()
		page.MonitorIDs = monitorIDs

		pages = append(pages, page)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating status pages: %w", err)
	}

	return &models.StatusPageListResponse{
		Items:    pages,
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}, nil
}

// UpdateStatusPage updates a status page (partial update)
func (s *Service) UpdateStatusPage(ctx context.Context, tenantID, pageID uuid.UUID, req *models.UpdateStatusPageRequest) (*models.StatusPage, error) {
	// Start transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Build update query dynamically
	setParts := []string{}
	args := []interface{}{}
	argIndex := 1

	if req.Slug != nil {
		setParts = append(setParts, fmt.Sprintf("slug = $%d", argIndex))
		args = append(args, *req.Slug)
		argIndex++
	}

	if req.Title != nil {
		setParts = append(setParts, fmt.Sprintf("title = $%d", argIndex))
		args = append(args, *req.Title)
		argIndex++
	}

	if req.Description != nil {
		setParts = append(setParts, fmt.Sprintf("description = $%d", argIndex))
		args = append(args, *req.Description)
		argIndex++
	}

	if req.LogoURL != nil {
		setParts = append(setParts, fmt.Sprintf("logo_url = $%d", argIndex))
		args = append(args, *req.LogoURL)
		argIndex++
	}

	if req.PrimaryColor != nil {
		setParts = append(setParts, fmt.Sprintf("primary_color = $%d", argIndex))
		args = append(args, *req.PrimaryColor)
		argIndex++
	}

	if req.SecondaryColor != nil {
		setParts = append(setParts, fmt.Sprintf("secondary_color = $%d", argIndex))
		args = append(args, *req.SecondaryColor)
		argIndex++
	}

	if req.Settings != nil {
		var existingJSON []byte
		row := tx.QueryRowContext(ctx, `SELECT settings FROM status_pages WHERE id = $1 AND tenant_id = $2`, pageID, tenantID)
		if err := row.Scan(&existingJSON); err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("status page not found")
			}
			return nil, fmt.Errorf("failed to get existing settings: %w", err)
		}

		stored := parseStatusPageSettings(existingJSON).applyPatch(req.Settings)
		mergedJSON, _ := json.Marshal(stored)

		setParts = append(setParts, fmt.Sprintf("settings = $%d", argIndex))
		args = append(args, mergedJSON)
		argIndex++
	}

	// Update monitor associations if provided
	if req.MonitorIDs != nil {
		// Delete existing associations
		deleteQuery := `DELETE FROM status_page_monitors WHERE status_page_id = $1`
		_, err := tx.ExecContext(ctx, deleteQuery, pageID)
		if err != nil {
			return nil, fmt.Errorf("failed to delete monitor associations: %w", err)
		}

		// Insert new associations
		if len(*req.MonitorIDs) > 0 {
			monitorUUIDs, err := s.validateAndParseMonitorIDs(ctx, tx, tenantID, *req.MonitorIDs)
			if err != nil {
				return nil, err
			}

			if len(monitorUUIDs) > 0 {
				insertQuery := `
					INSERT INTO status_page_monitors (status_page_id, monitor_id, position, display_name)
					VALUES ($1, $2, $3, $4)
				`
				for pos, monitorID := range monitorUUIDs {
					var displayName *string
					if req.MonitorDisplayNames != nil {
						if raw, ok := (*req.MonitorDisplayNames)[monitorID.String()]; ok {
							v := strings.TrimSpace(raw)
							if v != "" {
								displayName = &v
							}
						}
					}
					_, err := tx.ExecContext(ctx, insertQuery, pageID, monitorID, pos, displayName)
					if err != nil {
						if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
							// Duplicate key, skip
							continue
						}
						return nil, fmt.Errorf("failed to associate monitor: %w", err)
					}
				}
			}
		}
	}

	if len(setParts) > 0 {
		setParts = append(setParts, fmt.Sprintf("updated_at = NOW()"))

		// Add WHERE clause
		whereArgIndex := argIndex
		args = append(args, pageID, tenantID)

		setClause := ""
		for i, part := range setParts {
			if i > 0 {
				setClause += ", "
			}
			setClause += part
		}

		updateQuery := fmt.Sprintf(`
			UPDATE status_pages
			SET %s
			WHERE id = $%d AND tenant_id = $%d
		`, setClause, whereArgIndex, whereArgIndex+1)

		_, err = tx.ExecContext(ctx, updateQuery, args...)
		if err != nil {
			if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
				return nil, fmt.Errorf("slug already exists")
			}
			return nil, fmt.Errorf("failed to update status page: %w", err)
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Return updated page
	return s.GetStatusPage(ctx, tenantID, pageID)
}

// DeleteStatusPage deletes a status page
func (s *Service) DeleteStatusPage(ctx context.Context, tenantID, pageID uuid.UUID) error {
	query := `DELETE FROM status_pages WHERE id = $1 AND tenant_id = $2`
	result, err := s.db.ExecContext(ctx, query, pageID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to delete status page: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("status page not found")
	}

	return nil
}

// validateAndParseMonitorIDs validates that monitor IDs belong to the tenant and returns UUIDs
func (s *Service) validateAndParseMonitorIDs(ctx context.Context, tx *sql.Tx, tenantID uuid.UUID, monitorIDStrings []string) ([]uuid.UUID, error) {
	if len(monitorIDStrings) == 0 {
		return []uuid.UUID{}, nil
	}

	monitorUUIDs := make([]uuid.UUID, 0, len(monitorIDStrings))
	for _, idStr := range monitorIDStrings {
		monitorID, err := uuid.Parse(idStr)
		if err != nil {
			return nil, fmt.Errorf("invalid monitor ID: %s", idStr)
		}
		monitorUUIDs = append(monitorUUIDs, monitorID)
	}

	// Verify all monitors belong to the tenant
	query := `
		SELECT id FROM monitors
		WHERE id = ANY($1) AND tenant_id = $2
	`
	rows, err := tx.QueryContext(ctx, query, pq.Array(monitorUUIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to validate monitors: %w", err)
	}
	defer rows.Close()

	foundIDs := make(map[uuid.UUID]bool)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan monitor ID: %w", err)
		}
		foundIDs[id] = true
	}

	// Check if all IDs were found
	for _, id := range monitorUUIDs {
		if !foundIDs[id] {
			return nil, fmt.Errorf("monitor %s not found or does not belong to tenant", id)
		}
	}

	return monitorUUIDs, nil
}
