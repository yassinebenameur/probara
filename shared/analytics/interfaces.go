package analytics

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Reader exposes analytics queries used by higher-level services.
type Reader interface {
	GetScopeAnalytics(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, rangeValue Range, now time.Time) (*Result, error)
}
