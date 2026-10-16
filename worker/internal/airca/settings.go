package airca

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/ai"
)

// errAnalyzerMisconfigured reports that local AI is enabled for the workspace
// and the installation, but this worker has no analyzer: ai.NewAnalyzer
// rejected the client config at startup (LOCAL_AI_TOKEN under 16 characters,
// a malformed LOCAL_AI_URL). Terminal for the job, not a database error.
var errAnalyzerMisconfigured = errors.New("local AI client configuration was rejected at startup")

// analysisMisconfiguredMessage is aisettings.MessageMisconfigured (what
// GET /ai-settings reports for the same state) without the trailing period
// the analysis panel adds.
const analysisMisconfiguredMessage = "Local AI is not configured correctly for this installation"

// resolveAnalyzer returns the installation analyzer for an enabled
// workspace, (nil, nil) when local AI is disabled for the workspace or the
// installation, and errAnalyzerMisconfigured when it is enabled but this
// worker could not build its client.
func (c *Consumer) resolveAnalyzer(ctx context.Context, tenantID uuid.UUID) (ai.RootCauseAnalyzer, error) {
	enabled, err := ai.EffectiveEnabled(ctx, c.db, tenantID, c.cfg.LocalAI.Disabled)
	if err != nil || !enabled {
		return nil, err
	}
	if c.envAnalyzer == nil {
		return nil, errAnalyzerMisconfigured
	}
	return c.envAnalyzer, nil
}
