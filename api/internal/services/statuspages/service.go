package statuspages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

type statusPageSectionInput struct {
	Title    string
	Monitors []statusPageSectionMonitorInput
}

type statusPageSectionMonitorInput struct {
	MonitorID   string
	DisplayName *string
}

type statusPageDBTX interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

const defaultStatusPageSectionTitle = "Services"

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

// CreateStatusPage creates a new status page.
func (s *Service) CreateStatusPage(ctx context.Context, tenantID uuid.UUID, req *models.CreateStatusPageRequest) (*models.StatusPage, error) {
	pageID := uuid.New()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `
		INSERT INTO status_pages (
			id, tenant_id, slug, title, description, logo_url,
			primary_color, secondary_color, settings, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
	`

	settingsStored := defaultStatusPageSettings().applyPatch(req.Settings)
	settingsJSON, _ := json.Marshal(settingsStored)
	if _, err := tx.ExecContext(ctx, query,
		pageID, tenantID, req.Slug, req.Title, req.Description,
		req.LogoURL, req.PrimaryColor, req.SecondaryColor, settingsJSON,
	); err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return nil, fmt.Errorf("slug already exists")
		}
		return nil, fmt.Errorf("failed to create status page: %w", err)
	}

	sections := sectionsFromCreateRequest(req.Sections)
	if len(sections) == 0 {
		sections = legacySectionsFromMonitorIDs(req.MonitorIDs, req.MonitorDisplayNames)
	}
	if err := s.writeStatusPageSections(ctx, tx, tenantID, pageID, sections); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return s.GetStatusPage(ctx, tenantID, pageID)
}

// GetStatusPage retrieves a status page by ID (tenant-scoped).
func (s *Service) GetStatusPage(ctx context.Context, tenantID, pageID uuid.UUID) (*models.StatusPage, error) {
	query := `
		SELECT id, tenant_id, slug, title, description, logo_url,
			primary_color, secondary_color, settings, created_at, updated_at
		FROM status_pages
		WHERE id = $1 AND tenant_id = $2
	`

	var page models.StatusPage
	var settingsJSON []byte
	if err := s.db.QueryRowContext(ctx, query, pageID, tenantID).Scan(
		&page.ID, &page.TenantID, &page.Slug, &page.Title,
		&page.Description, &page.LogoURL, &page.PrimaryColor,
		&page.SecondaryColor, &settingsJSON, &page.CreatedAt, &page.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("status page not found")
		}
		return nil, fmt.Errorf("failed to get status page: %w", err)
	}

	page.Settings = parseStatusPageSettings(settingsJSON).toAPI()
	sections, err := s.loadStatusPageSections(ctx, pageID)
	if err != nil {
		return nil, err
	}
	page.Sections = sections
	page.MonitorIDs = flattenSectionMonitorIDs(sections)

	return &page, nil
}

// ListStatusPages lists status pages with pagination.
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

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM status_pages WHERE tenant_id = $1`, tenantID).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count status pages: %w", err)
	}

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

	pages := make([]models.StatusPage, 0)
	for rows.Next() {
		var item models.StatusPage
		var settingsJSON []byte
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.Slug, &item.Title,
			&item.Description, &item.LogoURL, &item.PrimaryColor,
			&item.SecondaryColor, &settingsJSON, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan status page: %w", err)
		}
		item.Settings = parseStatusPageSettings(settingsJSON).toAPI()

		sections, err := s.loadStatusPageSections(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		item.Sections = sections
		item.MonitorIDs = flattenSectionMonitorIDs(sections)
		pages = append(pages, item)
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

// UpdateStatusPage updates a status page (partial update).
func (s *Service) UpdateStatusPage(ctx context.Context, tenantID, pageID uuid.UUID, req *models.UpdateStatusPageRequest) (*models.StatusPage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	setParts := make([]string, 0)
	args := make([]interface{}, 0)
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
		if err := tx.QueryRowContext(ctx, `SELECT settings FROM status_pages WHERE id = $1 AND tenant_id = $2`, pageID, tenantID).Scan(&existingJSON); err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("status page not found")
			}
			return nil, fmt.Errorf("failed to get existing settings: %w", err)
		}
		mergedJSON, _ := json.Marshal(parseStatusPageSettings(existingJSON).applyPatch(req.Settings))
		setParts = append(setParts, fmt.Sprintf("settings = $%d", argIndex))
		args = append(args, mergedJSON)
		argIndex++
	}

	sectionsUpdated := false
	switch {
	case req.Sections != nil:
		sectionsUpdated = true
		if err := s.writeStatusPageSections(ctx, tx, tenantID, pageID, sectionsFromUpdateRequest(*req.Sections)); err != nil {
			return nil, err
		}
	case req.MonitorIDs != nil:
		sectionsUpdated = true
		displayNames := map[string]string{}
		if req.MonitorDisplayNames != nil {
			displayNames = *req.MonitorDisplayNames
		}
		if err := s.writeStatusPageSections(ctx, tx, tenantID, pageID, legacySectionsFromMonitorIDs(*req.MonitorIDs, displayNames)); err != nil {
			return nil, err
		}
	}

	if len(setParts) > 0 || sectionsUpdated {
		setParts = append(setParts, "updated_at = NOW()")
		whereArgIndex := argIndex
		args = append(args, pageID, tenantID)
		updateQuery := fmt.Sprintf(`
			UPDATE status_pages
			SET %s
			WHERE id = $%d AND tenant_id = $%d
		`, strings.Join(setParts, ", "), whereArgIndex, whereArgIndex+1)
		result, err := tx.ExecContext(ctx, updateQuery, args...)
		if err != nil {
			if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
				return nil, fmt.Errorf("slug already exists")
			}
			return nil, fmt.Errorf("failed to update status page: %w", err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("failed to get rows affected: %w", err)
		}
		if rowsAffected == 0 {
			return nil, fmt.Errorf("status page not found")
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return s.GetStatusPage(ctx, tenantID, pageID)
}

// DeleteStatusPage deletes a status page.
func (s *Service) DeleteStatusPage(ctx context.Context, tenantID, pageID uuid.UUID) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM status_pages WHERE id = $1 AND tenant_id = $2`, pageID, tenantID)
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

func sectionsFromCreateRequest(sections []models.StatusPageSection) []statusPageSectionInput {
	out := make([]statusPageSectionInput, 0, len(sections))
	for _, section := range sections {
		out = append(out, sectionInputFromModel(section))
	}
	return out
}

func sectionsFromUpdateRequest(sections []models.StatusPageSection) []statusPageSectionInput {
	out := make([]statusPageSectionInput, 0, len(sections))
	for _, section := range sections {
		out = append(out, sectionInputFromModel(section))
	}
	return out
}

func sectionInputFromModel(section models.StatusPageSection) statusPageSectionInput {
	out := statusPageSectionInput{
		Title:    strings.TrimSpace(section.Title),
		Monitors: make([]statusPageSectionMonitorInput, 0, len(section.Monitors)),
	}
	for _, monitor := range section.Monitors {
		out.Monitors = append(out.Monitors, statusPageSectionMonitorInput{
			MonitorID:   strings.TrimSpace(monitor.MonitorID),
			DisplayName: monitor.DisplayName,
		})
	}
	return out
}

func legacySectionsFromMonitorIDs(monitorIDs []string, displayNames map[string]string) []statusPageSectionInput {
	if len(monitorIDs) == 0 {
		return nil
	}
	section := statusPageSectionInput{
		Title:    defaultStatusPageSectionTitle,
		Monitors: make([]statusPageSectionMonitorInput, 0, len(monitorIDs)),
	}
	for _, rawID := range monitorIDs {
		monitorID := strings.TrimSpace(rawID)
		var displayName *string
		if raw, ok := displayNames[monitorID]; ok {
			trimmed := strings.TrimSpace(raw)
			if trimmed != "" {
				displayName = &trimmed
			}
		}
		section.Monitors = append(section.Monitors, statusPageSectionMonitorInput{
			MonitorID:   monitorID,
			DisplayName: displayName,
		})
	}
	return []statusPageSectionInput{section}
}

func (s *Service) loadStatusPageSections(ctx context.Context, pageID uuid.UUID) ([]models.StatusPageSection, error) {
	query := `
		SELECT id, title, position, created_at, updated_at
		FROM status_page_sections
		WHERE status_page_id = $1
		ORDER BY position ASC, created_at ASC, id ASC
	`
	rows, err := s.db.QueryContext(ctx, query, pageID)
	if err != nil {
		if isUndefinedTableError(err) {
			return s.loadLegacyStatusPageSections(ctx, pageID)
		}
		return nil, fmt.Errorf("failed to query status page sections: %w", err)
	}
	defer rows.Close()

	sections := make([]models.StatusPageSection, 0)
	for rows.Next() {
		var sectionID uuid.UUID
		var section models.StatusPageSection
		var createdAt time.Time
		var updatedAt time.Time
		if err := rows.Scan(&sectionID, &section.Title, &section.Position, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan status page section: %w", err)
		}
		section.ID = sectionID.String()
		section.CreatedAt = &createdAt
		section.UpdatedAt = &updatedAt
		monitors, err := s.loadStatusPageSectionMonitors(ctx, sectionID)
		if err != nil {
			return nil, err
		}
		section.Monitors = monitors
		sections = append(sections, section)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating status page sections: %w", err)
	}
	if len(sections) == 0 {
		return s.loadLegacyStatusPageSections(ctx, pageID)
	}
	return sections, nil
}

func (s *Service) loadStatusPageSectionMonitors(ctx context.Context, sectionID uuid.UUID) ([]models.StatusPageSectionMonitor, error) {
	query := `
		SELECT monitor_id, display_name, position
		FROM status_page_section_monitors
		WHERE section_id = $1
		ORDER BY position ASC, monitor_id ASC
	`
	rows, err := s.db.QueryContext(ctx, query, sectionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query section monitors: %w", err)
	}
	defer rows.Close()

	monitors := make([]models.StatusPageSectionMonitor, 0)
	for rows.Next() {
		var monitorID uuid.UUID
		var displayName sql.NullString
		var position int
		if err := rows.Scan(&monitorID, &displayName, &position); err != nil {
			return nil, fmt.Errorf("failed to scan section monitor: %w", err)
		}
		monitor := models.StatusPageSectionMonitor{
			MonitorID: monitorID.String(),
			Position:  position,
		}
		if displayName.Valid {
			trimmed := strings.TrimSpace(displayName.String)
			if trimmed != "" {
				monitor.DisplayName = &trimmed
			}
		}
		monitors = append(monitors, monitor)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating section monitors: %w", err)
	}
	return monitors, nil
}

func (s *Service) loadLegacyStatusPageSections(ctx context.Context, pageID uuid.UUID) ([]models.StatusPageSection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT monitor_id, display_name, position
		FROM status_page_monitors
		WHERE status_page_id = $1
		ORDER BY position ASC, monitor_id ASC
	`, pageID)
	if err != nil {
		return nil, fmt.Errorf("failed to query legacy status page monitors: %w", err)
	}
	defer rows.Close()

	monitors := make([]models.StatusPageSectionMonitor, 0)
	for rows.Next() {
		var monitorID uuid.UUID
		var displayName sql.NullString
		var position int
		if err := rows.Scan(&monitorID, &displayName, &position); err != nil {
			return nil, fmt.Errorf("failed to scan legacy status page monitor: %w", err)
		}
		monitor := models.StatusPageSectionMonitor{
			MonitorID: monitorID.String(),
			Position:  position,
		}
		if displayName.Valid {
			trimmed := strings.TrimSpace(displayName.String)
			if trimmed != "" {
				monitor.DisplayName = &trimmed
			}
		}
		monitors = append(monitors, monitor)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating legacy status page monitors: %w", err)
	}
	if len(monitors) == 0 {
		return nil, nil
	}
	return []models.StatusPageSection{{
		Title:    defaultStatusPageSectionTitle,
		Position: 0,
		Monitors: monitors,
	}}, nil
}

func flattenSectionMonitorIDs(sections []models.StatusPageSection) []uuid.UUID {
	out := make([]uuid.UUID, 0)
	for _, section := range sections {
		for _, monitor := range section.Monitors {
			monitorID, err := uuid.Parse(strings.TrimSpace(monitor.MonitorID))
			if err != nil {
				continue
			}
			out = append(out, monitorID)
		}
	}
	return out
}

func (s *Service) writeStatusPageSections(ctx context.Context, dbtx statusPageDBTX, tenantID, pageID uuid.UUID, sections []statusPageSectionInput) error {
	sectionsTableAvailable := true
	if _, err := dbtx.ExecContext(ctx, `DELETE FROM status_page_sections WHERE status_page_id = $1`, pageID); err != nil {
		if isUndefinedTableError(err) {
			sectionsTableAvailable = false
		} else {
			return fmt.Errorf("failed to clear status page sections: %w", err)
		}
	}

	monitorLookup, err := s.validateAndParseSectionMonitorIDs(ctx, dbtx, tenantID, sections)
	if err != nil {
		return err
	}

	if sectionsTableAvailable {
		insertSectionQuery := `
			INSERT INTO status_page_sections (id, status_page_id, title, position, created_at, updated_at)
			VALUES ($1, $2, $3, $4, NOW(), NOW())
		`
		insertMonitorQuery := `
			INSERT INTO status_page_section_monitors (section_id, monitor_id, position, display_name)
			VALUES ($1, $2, $3, $4)
		`
		for sectionPos, section := range sections {
			sectionID := uuid.New()
			title := strings.TrimSpace(section.Title)
			if title == "" {
				title = defaultStatusPageSectionTitle
			}
			if _, err := dbtx.ExecContext(ctx, insertSectionQuery, sectionID, pageID, title, sectionPos); err != nil {
				return fmt.Errorf("failed to insert status page section: %w", err)
			}
			for monitorPos, monitor := range section.Monitors {
				parsedID, ok := monitorLookup[monitor.MonitorID]
				if !ok {
					continue
				}
				displayName := trimOptionalString(monitor.DisplayName)
				if _, err := dbtx.ExecContext(ctx, insertMonitorQuery, sectionID, parsedID, monitorPos, displayName); err != nil {
					return fmt.Errorf("failed to insert status page section monitor: %w", err)
				}
			}
		}
	}

	return s.syncLegacyStatusPageMonitors(ctx, dbtx, pageID, sections, monitorLookup)
}

func (s *Service) syncLegacyStatusPageMonitors(ctx context.Context, dbtx statusPageDBTX, pageID uuid.UUID, sections []statusPageSectionInput, monitorLookup map[string]uuid.UUID) error {
	if _, err := dbtx.ExecContext(ctx, `DELETE FROM status_page_monitors WHERE status_page_id = $1`, pageID); err != nil {
		return fmt.Errorf("failed to clear legacy status page monitors: %w", err)
	}

	insertQuery := `
		INSERT INTO status_page_monitors (status_page_id, monitor_id, position, display_name)
		VALUES ($1, $2, $3, $4)
	`
	position := 0
	for _, section := range sections {
		for _, monitor := range section.Monitors {
			parsedID, ok := monitorLookup[monitor.MonitorID]
			if !ok {
				continue
			}
			if _, err := dbtx.ExecContext(ctx, insertQuery, pageID, parsedID, position, trimOptionalString(monitor.DisplayName)); err != nil {
				return fmt.Errorf("failed to sync legacy status page monitor: %w", err)
			}
			position++
		}
	}
	return nil
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func isUndefinedTableError(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	return pqErr.Code == "42P01"
}

// validateAndParseMonitorIDs validates that monitor IDs belong to the tenant and returns UUIDs.
func (s *Service) validateAndParseMonitorIDs(ctx context.Context, dbtx statusPageDBTX, tenantID uuid.UUID, monitorIDStrings []string) ([]uuid.UUID, error) {
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

	query := `
		SELECT id FROM monitors
		WHERE id = ANY($1) AND tenant_id = $2
	`
	rows, err := dbtx.QueryContext(ctx, query, pq.Array(monitorUUIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to validate monitors: %w", err)
	}
	defer rows.Close()

	foundIDs := make(map[uuid.UUID]bool, len(monitorUUIDs))
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan monitor ID: %w", err)
		}
		foundIDs[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitor validation rows: %w", err)
	}

	for _, id := range monitorUUIDs {
		if !foundIDs[id] {
			return nil, fmt.Errorf("monitor %s not found or does not belong to tenant", id)
		}
	}

	return monitorUUIDs, nil
}

func (s *Service) validateAndParseSectionMonitorIDs(ctx context.Context, dbtx statusPageDBTX, tenantID uuid.UUID, sections []statusPageSectionInput) (map[string]uuid.UUID, error) {
	rawIDs := make([]string, 0)
	seen := make(map[string]struct{})
	for _, section := range sections {
		for _, monitor := range section.Monitors {
			monitorID := strings.TrimSpace(monitor.MonitorID)
			if monitorID == "" {
				continue
			}
			if _, ok := seen[monitorID]; ok {
				continue
			}
			seen[monitorID] = struct{}{}
			rawIDs = append(rawIDs, monitorID)
		}
	}

	parsedIDs, err := s.validateAndParseMonitorIDs(ctx, dbtx, tenantID, rawIDs)
	if err != nil {
		return nil, err
	}

	out := make(map[string]uuid.UUID, len(rawIDs))
	for idx, rawID := range rawIDs {
		out[rawID] = parsedIDs[idx]
	}
	return out, nil
}
