// Query-parameter normalization and time-range math.

package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func normalizeOverviewParams(params *models.DashboardOverviewQuery) models.DashboardOverviewQuery {
	normalized := models.DashboardOverviewQuery{
		Range:         normalizeDashboardRange(""),
		FailuresLimit: defaultFailuresLimit,
		AlertsLimit:   defaultAlertsLimit,
	}
	if params == nil {
		return normalized
	}

	normalized.Range = normalizeDashboardRange(params.Range)

	if params.FailuresLimit > 0 {
		normalized.FailuresLimit = params.FailuresLimit
	}
	if normalized.FailuresLimit > maxListLimit {
		normalized.FailuresLimit = maxListLimit
	}

	if params.AlertsLimit > 0 {
		normalized.AlertsLimit = params.AlertsLimit
	}
	if normalized.AlertsLimit > maxListLimit {
		normalized.AlertsLimit = maxListLimit
	}

	normalized.Tags = normalizeDashboardTags(params.Tags)

	return normalized
}

func normalizeListParams(params *models.DashboardListQuery, defaultLimit int) models.DashboardListQuery {
	normalized := models.DashboardListQuery{
		Range: normalizeDashboardRange(""),
		Limit: defaultLimit,
	}
	if params == nil {
		return normalized
	}

	normalized.Range = normalizeDashboardRange(params.Range)
	if params.Limit > 0 {
		normalized.Limit = params.Limit
	}
	if normalized.Limit > maxListLimit {
		normalized.Limit = maxListLimit
	}
	normalized.Tags = normalizeDashboardTags(params.Tags)

	return normalized
}

func normalizeDashboardRange(rangeValue models.DashboardRange) models.DashboardRange {
	switch rangeValue {
	case models.DashboardRange1h, models.DashboardRange24h, models.DashboardRange7d, models.DashboardRange30d, models.DashboardRange90d, models.DashboardRange365d:
		return rangeValue
	default:
		return models.DashboardRange24h
	}
}

func isDashboardRollupRange(rangeValue models.DashboardRange) bool {
	switch rangeValue {
	case models.DashboardRange7d, models.DashboardRange30d, models.DashboardRange90d, models.DashboardRange365d:
		return true
	default:
		return false
	}
}

func normalizeDashboardTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}

	tagSet := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			continue
		}
		tagSet[trimmed] = struct{}{}
	}
	if len(tagSet) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(tagSet))
	for tag := range tagSet {
		normalized = append(normalized, tag)
	}
	sort.Strings(normalized)
	return normalized
}

func rangeBounds(rangeValue models.DashboardRange) (time.Time, time.Time, time.Duration, error) {
	now := time.Now().UTC()
	switch rangeValue {
	case models.DashboardRange1h:
		end := now.Truncate(5 * time.Minute)
		return end.Add(-55 * time.Minute), end, 5 * time.Minute, nil
	case models.DashboardRange24h:
		end := now.Truncate(time.Hour)
		return end.Add(-23 * time.Hour), end, time.Hour, nil
	case models.DashboardRange7d:
		end := truncateDayUTC(now)
		return end.AddDate(0, 0, -6), end, 24 * time.Hour, nil
	case models.DashboardRange30d:
		end := truncateDayUTC(now)
		return end.AddDate(0, 0, -29), end, 24 * time.Hour, nil
	case models.DashboardRange90d:
		end := truncateDayUTC(now)
		return end.AddDate(0, 0, -89), end, 24 * time.Hour, nil
	case models.DashboardRange365d:
		end := truncateDayUTC(now)
		return end.AddDate(0, 0, -364), end, 24 * time.Hour, nil
	default:
		return time.Time{}, time.Time{}, 0, fmt.Errorf("invalid dashboard range: %s", rangeValue)
	}
}

func truncateDayUTC(t time.Time) time.Time {
	return time.Date(t.UTC().Year(), t.UTC().Month(), t.UTC().Day(), 0, 0, 0, 0, time.UTC)
}
