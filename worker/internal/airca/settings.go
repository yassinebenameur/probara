package airca

import (
	"context"
	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/ai"
)

func (c *Consumer) resolveAnalyzer(ctx context.Context, tenantID uuid.UUID) (ai.RootCauseAnalyzer, error) {
	enabled, err := ai.EffectiveEnabled(ctx, c.db, tenantID, c.cfg.LocalAI.Disabled)
	if err != nil || !enabled {
		return nil, err
	}
	return c.envAnalyzer, nil
}
