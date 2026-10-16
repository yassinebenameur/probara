package slas

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/triageview"
	"github.com/yassinebenameur/probara/shared/ai"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
)

func TestAttributeCauses(t *testing.T) {
	m1, m2, m3 := uuid.New(), uuid.New(), uuid.New()
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	outage := func(m uuid.UUID, start time.Time, d time.Duration, planned float64) sharedanalytics.Outage {
		return sharedanalytics.Outage{MonitorID: &m, Start: start, End: start.Add(d), PlannedSeconds: planned}
	}
	alert := func(m uuid.UUID, trig time.Time, resolved *time.Time, c triageview.Cause) causeAlert {
		return causeAlert{ID: uuid.New(), MonitorID: m, TriggeredAt: trig, ResolvedAt: resolved, Cause: c}
	}
	dns := triageview.Cause{Kind: models.SLACauseSuspected, Key: "dns_failure"}
	dnsConfirmed := triageview.Cause{Kind: models.SLACauseSuspected, Key: "dns_failure", Verdict: ai.LabelVerdictConfirmed}
	tlsCorrected := triageview.Cause{Kind: models.SLACauseSuspected, Key: "tls_problem", Verdict: ai.LabelVerdictCorrected}
	pending := triageview.Cause{Kind: models.SLACausePending, Key: models.SLACausePending}
	untriaged := triageview.Cause{Kind: models.SLACauseUntriaged, Key: models.SLACauseUntriaged}

	outages := []sharedanalytics.Outage{
		// 0: m1, 1h. An untriaged reopened alert and the first alert's ready
		// triage overlap it: the named cause wins over time order.
		outage(m1, t0, time.Hour, 0),
		// 1: m1, 30m, a confirmed label.
		outage(m1, t0.Add(3*time.Hour), 30*time.Minute, 0),
		// 2: m2, 2m; the alert triggered 90s after it closed (slack).
		outage(m2, t0, 2*time.Minute, 0),
		// 3: m2, 10m, no alert at all (the next alert is past the slack).
		outage(m2, t0.Add(2*time.Hour), 10*time.Minute, 0),
		// 4: m3, 20m, triage still pending.
		outage(m3, t0, 20*time.Minute, 0),
		// 5: m3, fully planned: skipped.
		outage(m3, t0.Add(5*time.Hour), 10*time.Minute, 600),
		// 6: composite (no monitor): skipped.
		{Start: t0, End: t0.Add(time.Hour)},
		// 7: m1, 15m, corrected to a different cause.
		outage(m1, t0.Add(6*time.Hour), 15*time.Minute, 0),
	}
	firstDNS := alert(m1, t0.Add(-time.Minute), at(t0.Add(10*time.Minute)), dns)
	alerts := []causeAlert{
		alert(m1, t0.Add(20*time.Minute), at(t0.Add(time.Hour)), untriaged),
		firstDNS,
		alert(m1, t0.Add(3*time.Hour+time.Minute), at(t0.Add(3*time.Hour+30*time.Minute)), dnsConfirmed),
		alert(m2, t0.Add(3*time.Minute+30*time.Second), at(t0.Add(10*time.Minute)), untriaged),
		alert(m2, t0.Add(2*time.Hour+13*time.Minute), nil, pending),
		alert(m3, t0.Add(time.Minute), nil, pending),
		alert(m1, t0.Add(6*time.Hour), at(t0.Add(7*time.Hour)), tlsCorrected),
	}

	c, per := attributeCauses(outages, alerts)
	require.Len(t, per, len(outages))
	require.Equal(t, "dns_failure", per[0].Key)
	require.Equal(t, "DNS resolution failure", per[0].Title)
	require.Equal(t, "dns_failure", per[1].Key)
	require.Equal(t, models.SLACauseUntriaged, per[2].Kind, "an alert just after a short outage still matches it")
	require.Equal(t, models.SLACauseNoAlert, per[3].Kind)
	require.Equal(t, models.SLACausePending, per[4].Kind)
	require.Equal(t, outageCause{}, per[5], "planned-only outages are not attributed")
	require.Equal(t, outageCause{}, per[6], "composite outages are not attributed")
	require.Equal(t, "tls_problem", per[7].Key)

	require.Equal(t, 6, c.OutageCount)
	require.Equal(t, 3, c.TriagedOutageCount)
	require.Equal(t, 1, c.PendingOutageCount)
	require.InDelta(t, (60+30+2+10+20+15)*60, c.UnplannedSeconds, 1e-6)

	kinds := make([]string, 0, len(c.Buckets))
	for _, b := range c.Buckets {
		kinds = append(kinds, b.Key)
	}
	require.Equal(t, []string{"dns_failure", "tls_problem", models.SLACausePending, models.SLACauseUntriaged, models.SLACauseNoAlert}, kinds,
		"suspected causes by downtime, then the fixed buckets")

	dnsBucket := c.Buckets[0]
	require.Equal(t, 2, dnsBucket.OutageCount)
	require.Equal(t, 2, dnsBucket.AlertCount)
	require.Equal(t, 1, dnsBucket.MonitorCount)
	require.Equal(t, 1, dnsBucket.ConfirmedOutageCount)
	require.Equal(t, 0, dnsBucket.CorrectedOutageCount)
	require.InDelta(t, 90*60, dnsBucket.UnplannedSeconds, 1e-6)
	require.Equal(t, 1, c.Buckets[1].CorrectedOutageCount)
	require.Equal(t, "Not triaged", c.Buckets[3].Title)
	require.Equal(t, 0, c.Buckets[4].AlertCount, "no alert bucket counts no alerts")

	require.Contains(t, pendingNote(c), "1 monitor outage had an alert whose triage was still pending")
	require.Empty(t, pendingNote(&models.SLAReportSuspectedCauses{}))
}

// TestAttributeCausesChoiceAndCounts pins the per-outage choice (rank, then
// earliest, then id), alerts and monitors counted once per bucket, the slack
// boundary, and suspected buckets ordered by downtime rather than key.
func TestAttributeCausesChoiceAndCounts(t *testing.T) {
	m1, m2, m3, m4, m5 := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	outage := func(m uuid.UUID, start time.Time, d time.Duration) sharedanalytics.Outage {
		return sharedanalytics.Outage{MonitorID: &m, Start: start, End: start.Add(d)}
	}
	alert := func(id, m uuid.UUID, trig time.Time, resolved *time.Time, c triageview.Cause) causeAlert {
		return causeAlert{ID: id, MonitorID: m, TriggeredAt: trig, ResolvedAt: resolved, Cause: c}
	}
	dns := triageview.Cause{Kind: models.SLACauseSuspected, Key: "dns_failure"}
	tls := triageview.Cause{Kind: models.SLACauseSuspected, Key: "tls_problem"}
	inconclusive := triageview.Cause{Kind: models.SLACauseInconclusive, Key: models.SLACauseInconclusive}

	outages := []sharedanalytics.Outage{
		outage(m1, t0, 10*time.Minute),                     // 0: dns (spanning alert)
		outage(m1, t0.Add(20*time.Minute), 10*time.Minute), // 1: dns (same alert)
		outage(m2, t0, 30*time.Minute),                     // 2: dns, the earlier of two suspected
		outage(m3, t0, 20*time.Minute),                     // 3: tls, suspected beats an earlier inconclusive
		outage(m4, t0, 5*time.Minute),                      // 4: inconclusive; a dns alert at end+slack misses
		outage(m5, t0, time.Hour),                          // 5: tls, same trigger time, lower id
	}
	spanning, earlyDNS := uuid.New(), uuid.New()
	lo := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	hi := uuid.MustParse("ffffffff-0000-4000-8000-000000000001")
	alerts := []causeAlert{
		alert(spanning, m1, t0.Add(-time.Minute), at(t0.Add(40*time.Minute)), dns),
		alert(uuid.New(), m2, t0.Add(5*time.Minute), at(t0.Add(30*time.Minute)), tls),
		alert(earlyDNS, m2, t0.Add(time.Minute), at(t0.Add(2*time.Minute)), dns),
		alert(uuid.New(), m3, t0.Add(time.Minute), nil, inconclusive),
		alert(uuid.New(), m3, t0.Add(10*time.Minute), nil, tls),
		alert(uuid.New(), m4, t0, at(t0.Add(5*time.Minute)), inconclusive),
		alert(uuid.New(), m4, t0.Add(5*time.Minute+causeMatchSlack), nil, dns),
		alert(hi, m5, t0.Add(time.Minute), nil, dns),
		alert(lo, m5, t0.Add(time.Minute), nil, tls),
	}

	c, per := attributeCauses(outages, alerts)
	require.Len(t, per, len(outages))
	keys := make([]string, len(per))
	for i, p := range per {
		keys[i] = p.Key
	}
	require.Equal(t, []string{"dns_failure", "dns_failure", "dns_failure", "tls_problem", models.SLACauseInconclusive, "tls_problem"}, keys,
		"per-outage causes, parallel to the outages")

	require.Equal(t, 6, c.OutageCount)
	require.Equal(t, 6, c.TriagedOutageCount)
	require.Zero(t, c.PendingOutageCount)
	require.Empty(t, pendingNote(c))

	got := make([]string, 0, len(c.Buckets))
	for _, b := range c.Buckets {
		got = append(got, b.Key)
	}
	require.Equal(t, []string{"tls_problem", "dns_failure", models.SLACauseInconclusive}, got,
		"suspected buckets by unplanned time (80m before 50m), not by key")

	tlsBucket, dnsBucket := c.Buckets[0], c.Buckets[1]
	require.InDelta(t, 80*60, tlsBucket.UnplannedSeconds, 1e-6)
	require.Equal(t, 2, tlsBucket.OutageCount)
	require.Equal(t, 2, tlsBucket.AlertCount)
	require.Equal(t, 2, tlsBucket.MonitorCount)
	require.InDelta(t, 50*60, dnsBucket.UnplannedSeconds, 1e-6)
	require.Equal(t, 3, dnsBucket.OutageCount)
	require.Equal(t, 2, dnsBucket.AlertCount, "one alert spanning two outages counts once")
	require.Equal(t, 2, dnsBucket.MonitorCount)
	require.Equal(t, 1, c.Buckets[2].AlertCount, "the dns alert at end+slack does not match")
}

func TestPendingNote(t *testing.T) {
	require.Empty(t, pendingNote(nil))
	require.Empty(t, pendingNote(&models.SLAReportSuspectedCauses{}))
	require.Equal(t, "1 monitor outage had an alert whose triage was still pending when this report was computed; its suspected cause is listed as Triage pending.",
		pendingNote(&models.SLAReportSuspectedCauses{PendingOutageCount: 1}))
	require.Equal(t, "3 monitor outages had alerts whose triage was still pending when this report was computed; their suspected causes are listed as Triage pending.",
		pendingNote(&models.SLAReportSuspectedCauses{PendingOutageCount: 3}))
}

func TestAttributeCausesEmpty(t *testing.T) {
	c, per := attributeCauses(nil, nil)
	require.Empty(t, per)
	require.NotNil(t, c.Buckets, "buckets marshal as [], never null")
	require.Zero(t, c.OutageCount)
}
