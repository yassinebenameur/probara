package importservice

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/shared/db"
)

// GroupMembershipService writes the monitor_groups rows that back group
// membership. Narrowed to the one method the importer needs so tests can
// substitute it without standing up the whole group service.
type GroupMembershipService interface {
	AddMonitorsToGroup(ctx context.Context, tenantID, groupID uuid.UUID, monitorIDs []uuid.UUID) error
}

// Service handles monitor import operations
type Service struct {
	db             db.DB
	monitorService monitorservice.MonitorService
	groupService   GroupMembershipService
}

// NewService creates a new import service
func NewService(dbClient db.DB, monitorSvc monitorservice.MonitorService, groupSvc GroupMembershipService) *Service {
	return &Service{
		db:             dbClient,
		monitorService: monitorSvc,
		groupService:   groupSvc,
	}
}

// parsedSource is what every format parser returns. Beyond the rows, a parser
// for a foreign schema (Uptime Kuma, say) needs to report translation caveats
// and records it refused to translate at all, so those travel with the rows.
type parsedSource struct {
	Rows        []models.ImportRow
	Schema      string
	Warnings    []string
	SkippedRows []models.ImportSkippedRow
}
