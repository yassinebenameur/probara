package slas

// Suspected-cause breakdown of an SLA report's monitor outages. Each
// per-monitor outage with unplanned time takes the effective cause
// (triageview.EffectiveCause: operator label over model hypothesis) of the
// monitor's own availability alerts that overlap it. Downtime comes from
// sharedanalytics.Outages only, never from alert or check rows.
//
// Deliberate non-goals: incident_ai_analyses (operator-run, multi-monitor,
// no time anchor), group roll-up alerts on group monitors (a group has no
// timeline, S-U4) and alerts.root_cause_monitor_id (the dependency root is
// shown on the alert itself; it is not a catalogue cause).

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/triageview"
	"github.com/yassinebenameur/probara/shared/ai"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
)

// causeMatchSlack lets an alert that triggered just after a short outage
// closed still match it: the alerter stamps triggered_at after ingest wrote
// the down interval.
const causeMatchSlack = 2 * time.Minute

type causeAlert struct {
	ID          uuid.UUID
	MonitorID   uuid.UUID
	TriggeredAt time.Time
	ResolvedAt  *time.Time
	Cause       triageview.Cause
}

// outageCause is one outage's bucket; the zero value marks a span the
// breakdown skips (composite, or planned-only).
type outageCause struct {
	Kind, Key, Title string
}

// loadCauseAlerts returns the members' availability alerts that overlap the
// window (plus the match slack), with their effective causes.
func (s *Service) loadCauseAlerts(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, window sharedanalytics.TimeRange, now time.Time) ([]causeAlert, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.monitor_id, a.triggered_at, a.resolved_at, `+triageview.CauseColumns+`, `+triageview.ClaimableExpr("$5")+`
		FROM alerts a`+triageview.CauseJoin+`
		WHERE a.tenant_id = $1 AND a.kind = 'availability' AND a.monitor_id = ANY($2)
		  AND a.triggered_at < $4 AND (a.resolved_at IS NULL OR a.resolved_at > $3)
		ORDER BY a.monitor_id, a.triggered_at, a.id
	`, tenantID, pq.Array(monitorIDs), window.Start.UTC(), window.End.Add(causeMatchSlack).UTC(), now.Add(-ai.TriageLookback).UTC())
	if err != nil {
		return nil, fmt.Errorf("load SLA cause alerts: %w", err)
	}
	defer rows.Close()
	var out []causeAlert
	for rows.Next() {
		var a causeAlert
		var in triageview.CauseInput
		if err := rows.Scan(append([]any{&a.ID, &a.MonitorID, &a.TriggeredAt, &a.ResolvedAt}, in.Dest()...)...); err != nil {
			return nil, fmt.Errorf("load SLA cause alerts: %w", err)
		}
		a.Cause = triageview.EffectiveCause(in)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load SLA cause alerts: %w", err)
	}
	return out, nil
}

// causeRank orders candidate alerts of one outage: a named cause beats an
// inconclusive triage, which beats a pending one, which beats none. An
// operator who resolves an alert while the monitor is still down gets a
// reopened alert that the triage cooldown usually skips; ranking by quality
// before time keeps the first alert's ready triage for the whole outage.
var causeRank = map[string]int{
	models.SLACauseSuspected:    0,
	models.SLACauseInconclusive: 1,
	models.SLACausePending:      2,
	models.SLACauseUntriaged:    3,
}

// bucketOrder places the fixed buckets after the suspected causes.
var bucketOrder = map[string]int{
	models.SLACauseSuspected:    0,
	models.SLACauseInconclusive: 1,
	models.SLACausePending:      2,
	models.SLACauseUntriaged:    3,
	models.SLACauseNoAlert:      4,
}

func causeTitle(kind, key string) string {
	switch kind {
	case models.SLACauseSuspected:
		return ai.ChoiceTitle("rca", key)
	case models.SLACauseInconclusive:
		return "Inconclusive"
	case models.SLACausePending:
		return "Triage pending"
	case models.SLACauseUntriaged:
		return "Not triaged"
	default:
		return "No alert"
	}
}

func overlaps(a causeAlert, o sharedanalytics.Outage) bool {
	return a.TriggeredAt.Before(o.End.Add(causeMatchSlack)) && (a.ResolvedAt == nil || a.ResolvedAt.After(o.Start))
}

type causeAcc struct {
	bucket   models.SLAReportCauseBucket
	alerts   map[uuid.UUID]struct{}
	monitors map[uuid.UUID]struct{}
}

// attributeCauses rolls outages up by cause. Each per-monitor outage with
// unplanned time considers every availability alert of its monitor that
// overlaps it and takes the best-ranked one (earliest triggered on a tie, then
// id); its whole unplanned time goes to that cause. The returned slice is
// parallel to outages.
func attributeCauses(outages []sharedanalytics.Outage, alerts []causeAlert) (*models.SLAReportSuspectedCauses, []outageCause) {
	byMonitor := map[uuid.UUID][]causeAlert{}
	for _, a := range alerts {
		byMonitor[a.MonitorID] = append(byMonitor[a.MonitorID], a)
	}
	for _, list := range byMonitor {
		sort.SliceStable(list, func(i, j int) bool {
			if !list[i].TriggeredAt.Equal(list[j].TriggeredAt) {
				return list[i].TriggeredAt.Before(list[j].TriggeredAt)
			}
			return list[i].ID.String() < list[j].ID.String()
		})
	}

	out := &models.SLAReportSuspectedCauses{Buckets: []models.SLAReportCauseBucket{}}
	perOutage := make([]outageCause, len(outages))
	accs := map[string]*causeAcc{}
	for i, o := range outages {
		if o.MonitorID == nil || o.UnplannedSeconds() <= 0 {
			continue
		}
		var best *causeAlert
		for j := range byMonitor[*o.MonitorID] {
			a := &byMonitor[*o.MonitorID][j]
			if !overlaps(*a, o) {
				continue
			}
			if best == nil || causeRank[a.Cause.Kind] < causeRank[best.Cause.Kind] {
				best = a
			}
		}
		kind, key, verdict := models.SLACauseNoAlert, models.SLACauseNoAlert, ""
		if best != nil {
			kind, key, verdict = best.Cause.Kind, best.Cause.Key, best.Cause.Verdict
		}
		acc := accs[key]
		if acc == nil {
			acc = &causeAcc{
				bucket:   models.SLAReportCauseBucket{Kind: kind, Key: key, Title: causeTitle(kind, key)},
				alerts:   map[uuid.UUID]struct{}{},
				monitors: map[uuid.UUID]struct{}{},
			}
			accs[key] = acc
		}
		seconds := o.UnplannedSeconds()
		acc.bucket.OutageCount++
		acc.bucket.UnplannedSeconds += seconds
		acc.monitors[*o.MonitorID] = struct{}{}
		if best != nil {
			acc.alerts[best.ID] = struct{}{}
		}
		switch verdict {
		case ai.LabelVerdictConfirmed:
			acc.bucket.ConfirmedOutageCount++
		case ai.LabelVerdictCorrected:
			acc.bucket.CorrectedOutageCount++
		}
		perOutage[i] = outageCause{Kind: kind, Key: key, Title: acc.bucket.Title}

		out.OutageCount++
		out.UnplannedSeconds += seconds
		switch kind {
		case models.SLACauseSuspected, models.SLACauseInconclusive:
			out.TriagedOutageCount++
		case models.SLACausePending:
			out.PendingOutageCount++
		}
	}

	for _, acc := range accs {
		acc.bucket.AlertCount = len(acc.alerts)
		acc.bucket.MonitorCount = len(acc.monitors)
		out.Buckets = append(out.Buckets, acc.bucket)
	}
	sort.Slice(out.Buckets, func(i, j int) bool {
		a, b := out.Buckets[i], out.Buckets[j]
		if bucketOrder[a.Kind] != bucketOrder[b.Kind] {
			return bucketOrder[a.Kind] < bucketOrder[b.Kind]
		}
		if a.UnplannedSeconds != b.UnplannedSeconds {
			return a.UnplannedSeconds > b.UnplannedSeconds
		}
		if a.OutageCount != b.OutageCount {
			return a.OutageCount > b.OutageCount
		}
		return a.Key < b.Key
	})
	return out, perOutage
}

// pendingNote tells the reader that some causes were still being worked out
// when the report was computed (an issued report keeps them as pending).
func pendingNote(c *models.SLAReportSuspectedCauses) string {
	if c == nil || c.PendingOutageCount == 0 {
		return ""
	}
	if c.PendingOutageCount == 1 {
		return "1 monitor outage had an alert whose triage was still pending when this report was computed; its suspected cause is listed as Triage pending."
	}
	return fmt.Sprintf("%d monitor outages had alerts whose triage was still pending when this report was computed; their suspected causes are listed as Triage pending.", c.PendingOutageCount)
}
