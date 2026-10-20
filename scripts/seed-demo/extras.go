package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yassinebenameur/probara/shared/metricstore"
)

// ── alerts ───────────────────────────────────────────────────────────────

const alertInsert = `INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, acknowledged_at, resolved_at, failure_count, last_error,
	created_at, updated_at, root_cause_monitor_id, root_cause_down_since, kind, baseline_latency_ms, observed_latency_ms, anomaly_score,
	metric_name, metric_value, threshold_value, source_location_id, target_location_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$5,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`

func (w *world) writeAlerts(ctx context.Context, tx pgx.Tx) error {
	rootDown := map[string]time.Time{}
	if res := w.sims["orders-db"]; res != nil {
		for _, d := range res.downs {
			if d.event == "orders-db-failover" {
				rootDown["orders-db-failover"] = d.start
			}
		}
	}
	n := 0
	for i := range w.specs {
		s := &w.specs[i]
		res := w.sims[s.Key]
		if res == nil {
			continue
		}
		for _, d := range res.downs {
			to := w.now
			if d.end != nil {
				to = *d.end
			}
			if d.planned || w.maintCovers(s.Key, d.start, to) {
				continue
			}
			r := rngFor(s.Key+d.start.String(), 3)
			id := uuid.NewString()
			trig := d.start.Add(time.Duration(2+r.IntN(26)) * time.Second)
			var ack, resolved any
			status := "active"
			updated := trig
			if d.end != nil {
				status = "resolved"
				rt := d.end.Add(time.Duration(1+r.IntN(24)) * time.Second)
				resolved, updated = rt, rt
				dur := d.end.Sub(d.start)
				if dur > 4*time.Minute && r.Float64() < 0.6 {
					at := trig.Add(time.Duration(1+r.IntN(int(min(9, dur.Minutes()/2)))) * time.Minute)
					ack = at
				}
			} else if s.Key == "sftp-meridian" {
				status = "acknowledged"
				ack = trig.Add(18 * time.Minute)
				updated = trig.Add(18 * time.Minute)
			}
			var rc, rcSince any
			if d.event == "orders-db-failover" && s.Key != "orders-db" {
				rc, rcSince = w.ids["orders-db"], rootDown["orders-db-failover"]
			}
			fc := max(d.failures, s.Threshold)
			if _, err := tx.Exec(ctx, alertInsert, id, TenantID, w.ids[s.Key], status, trig, ack, resolved, fc, d.lastErr, updated, rc, rcSince,
				"availability", nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
				return err
			}
			n++
			ref := alertRef{id: id, monitorKey: s.Key, triggered: trig}
			if d.end != nil {
				rt := resolved.(time.Time)
				ref.resolved = &rt
			}
			w.alerts[d.event] = append(w.alerts[d.event], ref)
			if d.end == nil {
				w.openAlerts[s.Key] = id
				// Mark the already-due tenant-default notifications as sent so
				// the alerter does not fire them again on its first tick.
				for _, ch := range []struct {
					key   string
					delay time.Duration
				}{{"ops-bot", 0}, {"escalation", 15 * time.Minute}} {
					if w.now.Sub(trig) < ch.delay {
						continue
					}
					var ackSent any
					if ack != nil {
						ackSent = ack
					}
					if _, err := tx.Exec(ctx, `INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, acknowledged_sent_at)
						VALUES ($1,$2,$3,'created',$4)`, id, w.chans[ch.key], trig.Add(ch.delay+3*time.Second), ackSent); err != nil {
						return err
					}
				}
			}
		}
	}

	// A few non-availability kinds, all resolved history.
	type extra struct {
		kind, key                 string
		at                        time.Time
		dur                       time.Duration
		lastErr                   string
		baseline, observed, score any
		metric                    any
		value, threshold          any
		src, dst                  string
	}
	extras := []extra{
		{kind: "latency_anomaly", key: "recs-api", at: w.at(15, 18, 40), dur: 22 * time.Minute, baseline: 640.0, observed: 2140.0, score: 6.2},
		{kind: "latency_anomaly", key: "tax-api", at: w.at(3, 12, 5), dur: 9 * time.Minute, baseline: 610.0, observed: 1720.0, score: 4.8},
		{kind: "latency_anomaly", key: "search-api", at: w.at(48, 9, 22), dur: 8 * time.Minute, baseline: 74.0, observed: 690.0, score: 9.1},
		{kind: "host_metric", key: "host-db-orders", at: w.at(11, 2, 14), dur: 18 * time.Minute, metric: "system.cpu.utilization{state=used}", value: 0.94, threshold: 0.9},
		{kind: "host_metric", key: "host-db-orders", at: w.at(22, 13, 50), dur: 2*time.Hour + 5*time.Minute, metric: "system.filesystem.utilization{mountpoint=/var/lib/postgresql}", value: 0.97, threshold: 0.85},
		{kind: "host_metric", key: "host-sbc-01", at: w.at(29, 8, 31), dur: 34 * time.Minute, metric: "system.memory.utilization{state=used}", value: 0.95, threshold: 0.92},
		{kind: "tls_expiry", key: "cloudflare-status", at: w.at(44, 0, 3), dur: 26 * time.Hour, lastErr: "tls: expires in 13d (< 14d)", value: 13.0, threshold: 14.0},
		{kind: "mesh_edge", src: "sin", dst: "iad", at: w.at(6, 4, 2), dur: 7 * time.Minute, lastErr: "mesh probe: timeout: context deadline exceeded"},
	}
	for _, x := range extras {
		id := uuid.NewString()
		var mon, src, dst any
		if x.key != "" {
			mon = w.ids[x.key]
		}
		if x.src != "" {
			src, dst = w.locIDs[x.src], w.locIDs[x.dst]
		}
		fc := 0
		if x.kind == "mesh_edge" {
			fc = 3
		}
		var lastErr any
		if x.lastErr != "" {
			lastErr = x.lastErr
		}
		res := x.at.Add(x.dur)
		if _, err := tx.Exec(ctx, alertInsert, id, TenantID, mon, "resolved", x.at, nil, res, fc, lastErr, res, nil, nil,
			x.kind, x.baseline, x.observed, x.score, x.metric, x.value, x.threshold, src, dst); err != nil {
			return fmt.Errorf("%s alert: %w", x.kind, err)
		}
		n++
	}
	logf("  wrote %d alerts (%d open)", n, len(w.openAlerts))
	return nil
}

// ── status pages ─────────────────────────────────────────────────────────

type pageMon struct{ key, display string }

func (w *world) seedStatusPages(ctx context.Context, c *apiClient, pool *pgx.Conn) error {
	type section struct {
		title string
		mons  []pageMon
	}
	pages := []struct {
		key, slug, title, desc, primary, secondary, theme, footer string
		sections                                                  []section
		agent                                                     bool
	}{
		{"public", "northwind-status", "Northwind Status", "Live status of northwind.example — shopping, checkout, delivery and customer support.", "#ff6a13", "#0b0f14", "dark",
			"© 2026 Northwind Retail Ltd · Subscribe to updates at status@northwind.example", []section{
				{"Shop", []pageMon{{"sf-home", "Website"}, {"sf-pdp", "Product pages"}, {"sf-search", "Search"}, {"sf-basket", "Basket"}}},
				{"Checkout & payments", []pageMon{{"checkout-web", "Checkout"}, {"payment-auth", "Card payments"}, {"payment-paypal", "PayPal"}, {"payment-klarna", "Klarna"}, {"order-history", "Order history"}}},
				{"Delivery & returns", []pageMon{{"track-page", "Order tracking"}, {"returns", "Returns portal"}, {"g-carriers", "Carrier updates"}}},
				{"Account", []pageMon{{"login-page", "Sign-in"}, {"mfa-api", "Two-factor authentication"}}},
				{"Customer support", []pageMon{{"ivr", "Phone support"}, {"chat-ws", "Live chat"}}},
			}, false},
		{"partners", "northwind-partners", "Northwind Partner & Carrier APIs", "Availability of the APIs and file feeds our suppliers, marketplaces and carriers integrate with.", "#2563eb", "#0f172a", "light",
			"Questions? partners@northwind.example", []section{
				{"Partner APIs", []pageMon{{"api-gw", "API gateway"}, {"product-api", "Catalog API"}, {"orders-api", "Orders API"}, {"tracking-api", "Tracking API"}}},
				{"Carrier integrations", []pageMon{{"carrier-dhl", "DHL Express"}, {"carrier-dpd", "DPD"}, {"carrier-ups", "UPS"}, {"carrier-royalmail", "Royal Mail"}, {"carrier-evri", "Evri"}}},
				{"EDI & file feeds", []pageMon{{"sftp-meridian", "Meridian SFTP drop"}, {"edi-as2", "AS2 gateway"}, {"carrier-manifest", "Nightly manifest upload"}}},
			}, false},
		{"internal", "northwind-internal", "Northwind Platform (internal)", "Engineering view of shared platform components.", "#10b981", "#111827", "dark",
			"Owned by #platform-sre", []section{
				{"Core platform", []pageMon{{"ingress", ""}, {"nats", ""}, {"vault", ""}, {"consul", ""}, {"registry", ""}}},
				{"Data", []pageMon{{"g-data", "Data platform"}}},
				{"Voice", []pageMon{{"g-voice", "Voice & contact centre"}}},
				{"Hosts", []pageMon{{"host-web-01", ""}, {"host-web-02", ""}, {"host-db-orders", ""}, {"host-sbc-01", ""}}},
			}, true},
	}
	for _, p := range pages {
		var secs []map[string]any
		for si, s := range p.sections {
			var mons []map[string]any
			for mi, m := range s.mons {
				e := map[string]any{"monitor_id": w.ids[m.key], "position": mi}
				if m.display != "" {
					e["display_name"] = m.display
				}
				mons = append(mons, e)
			}
			secs = append(secs, map[string]any{"title": s.title, "position": si, "monitors": mons})
		}
		body := map[string]any{"slug": p.slug, "title": p.title, "description": p.desc, "primary_color": p.primary, "secondary_color": p.secondary,
			"sections": secs, "settings": map[string]any{"show_monitor_tags": false, "show_monitor_url": false, "show_monitor_uptime": true, "show_monitor_tls": true,
				"show_latency_charts": true, "show_agent_metrics": p.agent, "show_global_uptime": true, "show_footer": true, "footer_text": p.footer,
				"default_theme": p.theme, "allow_theme_toggle": true}}
		var r idResp
		if err := c.do(ctx, "POST", "/api/v1/status-pages", body, &r); err != nil {
			return err
		}
		w.pages[p.key] = r.ID
	}
	_, err := pool.Exec(ctx, `UPDATE status_pages SET created_at = $2 WHERE tenant_id = $1`, TenantID, w.start.Add(3*24*time.Hour))
	return err
}

// ── incidents ────────────────────────────────────────────────────────────

type tl struct {
	off      time.Duration
	typ, msg string
	meta     map[string]any
}

type incident struct {
	title, summary, severity, owner string
	created                         time.Time
	state                           string
	resolvedAfter                   time.Duration // 0 = open
	event                           string        // alerts of this event get linked
	monitors                        []string
	publish                         map[string][]string // page -> monitor keys
	unpublishAfter                  time.Duration
	timeline                        []tl
	ai                              bool
}

func (w *world) seedIncidents(ctx context.Context, _ *apiClient, pool *pgx.Conn) error {
	m := time.Minute
	big := w.events["orders-db-failover"]
	retStart := w.now.Add(-(2*time.Hour + 13*time.Minute))
	if res := w.sims["returns"]; res != nil && len(res.downs) > 0 {
		retStart = res.downs[len(res.downs)-1].start
	}
	incs := []incident{
		{title: "Orders database failover — checkout and order APIs failing", severity: "critical", owner: "maya.chen",
			summary: "The orders PostgreSQL primary stopped answering and the replica was promoted manually. Checkout, order placement and order history returned errors for ~47 minutes.",
			created: big.Add(5 * m), state: "resolved", resolvedAfter: 73 * m, event: "orders-db-failover",
			monitors: []string{"orders-db", "checkout-api", "orders-api", "checkout-web", "order-history"},
			publish:  map[string][]string{"public": {"checkout-web", "order-history"}},
			timeline: []tl{
				{2 * m, "public_update", "We're investigating errors affecting checkout and order placement. Some customers may be unable to complete purchases.", nil},
				{6 * m, "internal_note", "orders-db-01 stopped answering health checks at 14:12 UTC. Patroni has not promoted yet; replica lag 2.1s. Paging DBA on-call.", nil},
				{11 * m, "system", "Incident moved to identified", nil},
				{11 * m, "public_update", "We've identified a database failover in progress. Checkout and order history are affected; browsing is unaffected.", nil},
				{19 * m, "internal_note", "Manual promotion of orders-db-02 complete. PgBouncer pools still pinned to the old primary, restarting pgbouncer on the app nodes.", nil},
				{44 * m, "system", "Incident moved to monitoring", nil},
				{44 * m, "public_update", "A fix has been applied and checkout is recovering. We're monitoring error rates closely.", nil},
				{73 * m, "public_update", "Resolved: checkout and order APIs have been fully operational since 15:01 UTC. A post-incident review will follow.", nil},
				{2*24*time.Hour + 3*time.Hour, "internal_note", "Postmortem PM-2026-031 published: WAL volume on orders-db-01 filled up after a stuck replication slot. Added a host-metric rule on /var/lib/postgresql (>= 85%) and slot-lag alerting.", nil},
			}, ai: true},
		{title: "Search results timing out", severity: "high", owner: "priya.raman",
			summary: "Search node 2 lost its data volume mount; the cluster went red and search and autocomplete timed out until shards were reallocated.",
			created: w.events["search-timeouts"].Add(4 * m), state: "resolved", resolvedAfter: 31 * m, event: "search-timeouts",
			monitors: []string{"search-api", "search-suggest", "search-node-2"}, publish: map[string][]string{"public": {"sf-search"}}, unpublishAfter: 26 * time.Hour,
			timeline: []tl{
				{1 * m, "public_update", "Search is currently slow or unavailable. Browsing categories still works.", nil},
				{9 * m, "internal_note", "search-node-2: EBS volume detached (AWS event). Cluster red, 14 unassigned primaries.", nil},
				{12 * m, "system", "Incident moved to identified", nil},
				{20 * m, "system", "Incident moved to monitoring", nil},
				{20 * m, "public_update", "Search has recovered. We're monitoring while the cluster rebalances.", nil},
			}},
		{title: "SSO login failures for EU customers", severity: "high", owner: "lucas.martin",
			summary: "A bad identity-api release rejected refresh tokens signed with the previous key, logging customers out and failing sign-in.",
			created: w.events["sso-outage"].Add(3 * m), state: "resolved", resolvedAfter: 26 * m, event: "sso-outage",
			monitors: []string{"identity-api", "login-page", "oauth-token"},
			timeline: []tl{
				{2 * m, "internal_note", "identity-api 4.8.0 rolled out at 22:01. Error rate on /oauth2/token jumped to 41%.", nil},
				{5 * m, "system", "Incident moved to identified", nil},
				{6 * m, "internal_note", "Rolling back to 4.7.3.", nil},
				{14 * m, "system", "Incident moved to monitoring", nil},
			}},
		{title: "DHL tracking updates delayed", severity: "medium", owner: "lucas.martin",
			summary: "DHL's webhook endpoint timed out; tracking events queued and were replayed once the relay recovered.",
			created: w.events["dhl-tracking"].Add(6 * m), state: "resolved", resolvedAfter: 48 * m, event: "dhl-tracking",
			monitors: []string{"carrier-dhl", "tracking-api"}, publish: map[string][]string{"partners": {"carrier-dhl", "tracking-api"}},
			timeline: []tl{
				{1 * m, "public_update", "Tracking updates for DHL Express parcels are delayed. Parcels are moving normally.", nil},
				{15 * m, "system", "Incident moved to monitoring", nil},
				{15 * m, "public_update", "DHL has resolved the issue on their side; we are replaying 18,400 queued tracking events.", nil},
				{48 * m, "public_update", "All delayed tracking events have been delivered.", nil},
			}},
		{title: "Inbound calls failing on BT trunk", severity: "medium", owner: "priya.raman",
			summary: "BT trunk B stopped answering OPTIONS; inbound calls on 0330 numbers failed over to trunk A after ~9 minutes.",
			created: w.events["trunk-b-flap"].Add(4 * m), state: "resolved", resolvedAfter: 37 * m, event: "trunk-b-flap",
			monitors: []string{"sip-trunk-b"},
			timeline: []tl{
				{3 * m, "internal_note", "BT NOC ticket 7731-445 raised. SBC shows 408s towards 192.0.2.44.", nil},
				{12 * m, "system", "Incident moved to monitoring", nil},
			}},
		{title: "Card authorisations timing out (Adyen)", severity: "high", owner: "maya.chen",
			summary: "Authorisation requests to Adyen timed out for a short period; affected customers saw a 'payment failed' message and could retry.",
			created: w.events["psp-timeouts"].Add(1 * m), state: "resolved", resolvedAfter: 21 * m, event: "psp-timeouts",
			monitors: []string{"payment-auth", "payments-api"},
			timeline: []tl{
				{2 * m, "internal_note", "Adyen status page shows degraded performance in EU-West. Failing over is not possible for 3DS2 flows.", nil},
				{8 * m, "system", "Incident moved to monitoring", nil},
				{21 * m, "internal_note", "Error budget for 'Payments authorisation — 99.9%' is now below 25% for the month.", nil},
			}},
		{title: "Returns portal unavailable", severity: "high", owner: "lucas.martin",
			summary: "The returns portal returns HTTP 503 after the 4.12.0 release. Customers can still start a return through customer support.",
			created: retStart.Add(7 * m), state: "identified", event: "",
			monitors: []string{"returns"}, publish: map[string][]string{"public": {"returns"}},
			timeline: []tl{
				{2 * m, "public_update", "We're aware the returns portal is unavailable. You can still start a return by contacting our support team.", nil},
				{14 * m, "internal_note", "returns-svc pods in CrashLoopBackOff after the 4.12.0 deploy: config map is missing RMA_SIGNING_KEY.", nil},
				{21 * m, "system", "Incident moved to identified", nil},
				{22 * m, "public_update", "We've identified the cause and are rolling back the latest release.", nil},
				{58 * m, "internal_note", "Rollback blocked: migration 0042 already ran and is not reversible. Logistics team is preparing a forward fix.", nil},
			}},
	}
	for _, inc := range incs {
		if err := w.insertIncident(ctx, pool, inc); err != nil {
			return fmt.Errorf("%s: %w", inc.title, err)
		}
	}
	return nil
}

func (w *world) insertIncident(ctx context.Context, pool *pgx.Conn, inc incident) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	id := uuid.NewString()
	var resolvedAt any
	last := inc.created
	if inc.resolvedAfter > 0 {
		resolvedAt = inc.created.Add(inc.resolvedAfter)
	}
	for _, e := range inc.timeline {
		if t := inc.created.Add(e.off); t.After(last) {
			last = t
		}
	}
	if last.After(w.now) {
		last = w.now.Add(-time.Minute)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO incidents (id, tenant_id, title, summary, state, resolved_at, created_at, updated_at, severity, owner_user_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, TenantID, inc.title, inc.summary, inc.state, resolvedAt, inc.created, last, inc.severity, w.users[inc.owner]); err != nil {
		return err
	}
	entry := func(at time.Time, typ, msg string, meta map[string]any) error {
		if at.After(w.now) {
			return nil
		}
		if meta == nil {
			meta = map[string]any{}
		}
		b, _ := json.Marshal(meta)
		_, err := tx.Exec(ctx, `INSERT INTO incident_timeline_entries (id, tenant_id, incident_id, entry_type, message, metadata, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			uuid.NewString(), TenantID, id, typ, msg, string(b), at)
		return err
	}
	if err := entry(inc.created, "system", "Incident created", nil); err != nil {
		return err
	}
	for i, k := range inc.monitors {
		at := inc.created.Add(time.Duration(i+1) * 5 * time.Second)
		if _, err := tx.Exec(ctx, `INSERT INTO incident_monitors (incident_id, monitor_id, created_at) VALUES ($1,$2,$3)`, id, w.ids[k], at); err != nil {
			return err
		}
		if err := entry(at, "system", "Monitor attached", map[string]any{"monitor_id": w.ids[k]}); err != nil {
			return err
		}
	}
	var alerts []alertRef
	if inc.event != "" {
		alerts = w.alerts[inc.event]
	} else {
		for _, k := range inc.monitors {
			if aid, ok := w.openAlerts[k]; ok {
				alerts = append(alerts, alertRef{id: aid, monitorKey: k})
			}
		}
	}
	var lastResolved time.Time
	allResolved := len(alerts) > 0
	for i, a := range alerts {
		at := inc.created.Add(time.Duration(i+1)*7*time.Second + 30*time.Second)
		if _, err := tx.Exec(ctx, `INSERT INTO incident_alerts (incident_id, alert_id, created_at) VALUES ($1,$2,$3)`, id, a.id, at); err != nil {
			return err
		}
		if err := entry(at, "system", "Alert attached", map[string]any{"alert_id": a.id}); err != nil {
			return err
		}
		if a.resolved == nil {
			allResolved = false
		} else if a.resolved.After(lastResolved) {
			lastResolved = *a.resolved
		}
	}
	if allResolved && !lastResolved.IsZero() {
		if lastResolved.Before(inc.created) {
			lastResolved = inc.created.Add(time.Minute)
		}
		if err := entry(lastResolved, "system", "All linked alerts recovered", map[string]any{"alert_id": alerts[len(alerts)-1].id, "resolved_after": lastResolved.Sub(inc.created).String()}); err != nil {
			return err
		}
	}
	for _, e := range inc.timeline {
		if err := entry(inc.created.Add(e.off), e.typ, e.msg, e.meta); err != nil {
			return err
		}
	}
	if inc.resolvedAfter > 0 {
		if err := entry(inc.created.Add(inc.resolvedAfter), "system", "Incident resolved", nil); err != nil {
			return err
		}
	}
	for page, keys := range inc.publish {
		pid := w.pages[page]
		pubAt := inc.created.Add(90 * time.Second)
		var unpub any
		if inc.unpublishAfter > 0 {
			unpub = inc.created.Add(inc.unpublishAfter)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO incident_status_page_publications (incident_id, status_page_id, tenant_id, published_at, unpublished_at, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$4,$4)`, id, pid, TenantID, pubAt, unpub); err != nil {
			return err
		}
		var mids []string
		for _, k := range keys {
			mids = append(mids, w.ids[k])
			if _, err := tx.Exec(ctx, `INSERT INTO incident_status_page_monitors (incident_id, status_page_id, monitor_id, created_at) VALUES ($1,$2,$3,$4)`, id, pid, w.ids[k], pubAt); err != nil {
				return err
			}
			// Publishing needs the monitor linked to the incident too.
			if _, err := tx.Exec(ctx, `INSERT INTO incident_monitors (incident_id, monitor_id, created_at) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, id, w.ids[k], pubAt); err != nil {
				return err
			}
		}
		if err := entry(pubAt, "system", "Incident published to status page", map[string]any{"status_page_id": pid, "monitor_ids": mids}); err != nil {
			return err
		}
		if inc.unpublishAfter > 0 {
			if err := entry(inc.created.Add(inc.unpublishAfter), "system", "Incident unpublished from status page", map[string]any{"status_page_id": pid}); err != nil {
				return err
			}
		}
	}
	if inc.ai {
		ev, _ := json.Marshal(map[string]any{"alerts": len(alerts), "monitors": inc.monitors, "window": "14:05–15:10 UTC"})
		cf, _ := json.Marshal([]string{
			"orders-db-01 WAL volume reached 100% after a logical replication slot for the analytics CDC stopped advancing",
			"Patroni failover was blocked by maximum_lag_on_failover (1 MB) while the replica replayed WAL",
			"PgBouncer kept server connections pinned to the old primary after promotion",
		})
		ra, _ := json.Marshal([]string{
			"Alert on pg_replication_slots lag and on /var/lib/postgresql utilisation",
			"Drop or cap inactive replication slots (max_slot_wal_keep_size)",
			"Point PgBouncer at the Patroni leader endpoint instead of a static host",
		})
		if _, err := tx.Exec(ctx, `INSERT INTO incident_ai_analyses (id, tenant_id, incident_id, status, model, summary, probable_root_cause, contributing_factors,
				recommended_actions, confidence, evidence, requested_by, created_at, completed_at) VALUES ($1,$2,$3,'ready',$4,$5,$6,$7,$8,'high',$9,$10,$11,$12)`,
			uuid.NewString(), TenantID, id, "qwen3-30b-a3b",
			"Orders DB primary became unavailable at 14:12 UTC; five dependent checkout and order monitors failed within two minutes, all tracing to orders-db.",
			"Disk exhaustion of the WAL volume on orders-db-01 caused by a stalled replication slot, followed by a delayed failover.",
			string(cf), string(ra), string(ev), w.users["maya.chen"], inc.created.Add(20*time.Minute), inc.created.Add(20*time.Minute+38*time.Second)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ── SLAs ─────────────────────────────────────────────────────────────────

func (w *world) seedSLAs(ctx context.Context, c *apiClient, pool *pgx.Conn) error {
	ids := func(keys ...string) []string {
		var out []string
		for _, k := range keys {
			out = append(out, w.ids[k])
		}
		return out
	}
	slas := []struct {
		key  string
		body map[string]any
	}{
		{"checkout", map[string]any{"name": "Checkout — Enterprise 99.95%", "description": "Contracted availability of the checkout journey (B2C terms §4.2).",
			"target_pct": 99.95, "aggregation": "serial", "period": "monthly", "timezone": "UTC", "monitor_ids": ids("checkout-web", "checkout-api", "cart-api", "checkout-journey")}},
		{"storefront", map[string]any{"name": "Storefront availability 99.9%", "description": "Average availability of every storefront-tagged monitor.",
			"target_pct": 99.9, "aggregation": "mean", "period": "monthly", "timezone": "Europe/London", "tags": []string{"storefront"}}},
		{"payments", map[string]any{"name": "Payments authorisation — 99.9%", "description": "Card authorisation path, measured end to end.",
			"target_pct": paymentsSLATarget, "aggregation": "serial", "period": "monthly", "timezone": "UTC", "monitor_ids": ids("payments-api", "payment-auth", "payments-3ds")}},
		{"voice", map[string]any{"name": "Voice platform — EU enterprise", "description": "Quarterly commitment for inbound telephony and contact-centre tooling.",
			"target_pct": 99.9, "aggregation": "serial", "period": "quarterly", "timezone": "Europe/Paris", "tags": []string{"voice"}}},
	}
	for _, s := range slas {
		var r idResp
		if err := c.do(ctx, "POST", "/api/v1/slas", s.body, &r); err != nil {
			return err
		}
		w.slaIDs[s.key] = r.ID
	}
	if _, err := pool.Exec(ctx, `UPDATE slas SET created_at = $2, updated_at = $2 WHERE tenant_id = $1`, TenantID, w.start.Add(5*24*time.Hour)); err != nil {
		return err
	}
	lastMonth := w.monthStart().AddDate(0, -1, 0)
	monthKey := fmt.Sprintf("%04d-%02d", lastMonth.Year(), int(lastMonth.Month()))
	q := (int(w.now.Month())-1)/3 + 1
	qy := w.now.Year()
	q--
	if q == 0 {
		q, qy = 4, qy-1
	}
	quarterKey := fmt.Sprintf("%04d-Q%d", qy, q)
	issue := []struct{ sla, period string }{{"checkout", monthKey}, {"storefront", monthKey}, {"payments", monthKey}, {"voice", quarterKey}}
	for _, is := range issue {
		if err := c.do(ctx, "POST", "/api/v1/slas/"+w.slaIDs[is.sla]+"/reports", map[string]any{"period": is.period}, nil); err != nil {
			return fmt.Errorf("issue %s %s: %w", is.sla, is.period, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE sla_reports SET issued_at = $3 WHERE sla_id = $1 AND period_key = $2`,
			w.slaIDs[is.sla], is.period, w.monthStart().Add(26*time.Hour+17*time.Minute)); err != nil {
			return err
		}
	}
	return nil
}

// ── mesh + agent metrics ─────────────────────────────────────────────────

func (w *world) seedMeshAndMetrics(ctx context.Context, pool *pgx.Conn, sqldb *sql.DB) error {
	// Six hours of mesh probes between the worker locations, so the matrix
	// sparklines have history before the live workers add to it. Latency is
	// the inter-region round trip the live mesh endpoints add (meshLatency).
	var mesh []string
	for _, l := range locationCatalog() {
		if l.Worker && l.MeshOff != 0 {
			mesh = append(mesh, l.Key)
		}
	}
	var rows [][]any
	tid := uuid.MustParse(TenantID)
	for _, src := range mesh {
		for _, dst := range mesh {
			if src == dst {
				continue
			}
			r := rngFor(src+dst, 5)
			for t := w.now.Add(-6 * time.Hour); t.Before(w.now.Add(-30 * time.Second)); t = t.Add(30 * time.Second) {
				rows = append(rows, []any{uuid.New(), tid, uuid.MustParse(w.locIDs[src]), uuid.MustParse(w.locIDs[dst]), uuid.New(), "success", int32(meshLatency(src, dst, r)), nil,
					t.Add(time.Duration(r.IntN(2000)) * time.Millisecond)})
			}
		}
	}
	if _, err := pool.CopyFrom(ctx, pgx.Identifier{"mesh_probe_results"}, []string{"id", "tenant_id", "source_location_id", "target_location_id", "job_id", "status", "latency_ms", "error_message", "created_at"}, pgx.CopyFromRows(rows)); err != nil {
		return fmt.Errorf("mesh: %w", err)
	}

	// 24h of host metrics at 60s resolution through the metric store's own
	// write path (series registry, partitions, rollup marks).
	from := w.now.Add(-24 * time.Hour)
	if err := metricstore.EnsurePartitions(ctx, sqldb, from, w.now.Add(24*time.Hour)); err != nil {
		return err
	}
	for key := range hosts {
		mid := uuid.MustParse(w.ids[key])
		tx, err := sqldb.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		cache := map[string]int64{}
		var samples []metricstore.Sample
		hours := map[time.Time]bool{}
		for t := from; t.Before(w.now.Add(-time.Minute)); t = t.Add(time.Minute) {
			for _, p := range hostPoints(key, t) {
				ck := p.name + fmt.Sprint(p.attrs)
				sid, ok := cache[ck]
				if !ok {
					temp := "unspecified"
					if p.typ == "sum" {
						temp = "cumulative"
					}
					sid, err = metricstore.ResolveSeries(ctx, tx, metricstore.SeriesKey{TenantID: tid, MonitorID: mid, MetricName: p.name, Unit: p.unit,
						MetricType: p.typ, IsMonotonic: p.mono, Temporality: temp, Attributes: p.attrs})
					if err != nil {
						tx.Rollback()
						return err
					}
					cache[ck] = sid
				}
				samples = append(samples, metricstore.Sample{SeriesID: sid, TS: t, Value: p.v})
			}
			hours[t.Truncate(time.Hour)] = true
		}
		if _, err := metricstore.InsertSamples(ctx, tx, samples); err != nil {
			tx.Rollback()
			return err
		}
		var hl []time.Time
		for h := range hours {
			hl = append(hl, h)
		}
		sort.Slice(hl, func(i, j int) bool { return hl[i].Before(hl[j]) })
		if err := metricstore.MarkDirty(ctx, tx, mid, hl); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

type mpoint struct {
	name, unit, typ string
	mono            bool
	attrs           map[string]string
	v               float64
}

// hostPoints is one OTLP export of a host, shared by the backfill and the
// live loop in targets.
func hostPoints(key string, t time.Time) []mpoint {
	s := sampleHost(key, t)
	a := func(kv ...string) map[string]string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	pts := []mpoint{
		{"system.cpu.utilization", "1", "gauge", false, a("state", "used"), s.cpu},
		{"system.cpu.logical.count", "{cpu}", "sum", false, a(), float64(s.cores)},
		{"system.memory.usage", "By", "sum", false, a("state", "used"), s.memUsed},
		{"system.memory.usage", "By", "sum", false, a("state", "free"), s.memTotal - s.memUsed},
		{"system.memory.utilization", "1", "gauge", false, a("state", "used"), s.memUsed / s.memTotal},
		{"system.paging.usage", "By", "sum", false, a("state", "used"), s.swapUsed},
		{"system.paging.usage", "By", "sum", false, a("state", "free"), s.swapTotal - s.swapUsed},
		{"system.cpu.load_average.1m", "{thread}", "gauge", false, a(), s.load1},
		{"system.cpu.load_average.5m", "{thread}", "gauge", false, a(), s.load5},
		{"system.cpu.load_average.15m", "{thread}", "gauge", false, a(), s.load15},
		{"system.disk.io", "By", "sum", true, a("direction", "read"), s.diskR},
		{"system.disk.io", "By", "sum", true, a("direction", "write"), s.diskW},
		{"system.network.io", "By", "sum", true, a("direction", "receive"), s.netRx},
		{"system.network.io", "By", "sum", true, a("direction", "transmit"), s.netTx},
		{"system.processes.count", "{process}", "sum", false, a(), float64(s.procs)},
		{"system.uptime", "s", "gauge", false, a(), s.uptime},
	}
	mounts := make([]string, 0, len(s.fs))
	for mp := range s.fs {
		mounts = append(mounts, mp)
	}
	sort.Strings(mounts)
	for _, mp := range mounts {
		v := s.fs[mp]
		pts = append(pts,
			mpoint{"system.filesystem.usage", "By", "sum", false, a("mountpoint", mp, "state", "used"), v[0]},
			mpoint{"system.filesystem.usage", "By", "sum", false, a("mountpoint", mp, "state", "free"), v[1]},
			mpoint{"system.filesystem.utilization", "1", "gauge", false, a("mountpoint", mp), v[0] / (v[0] + v[1])},
		)
	}
	return pts
}

// ── audit trail ──────────────────────────────────────────────────────────

func (w *world) seedAudit(ctx context.Context, _ *apiClient, pool *pgx.Conn) error {
	// The structure seed created through the API was audited as it
	// happened: a burst of a few hundred admin rows stamped "now" that would
	// bury the realistic recent activity on page 1. Move the burst, order
	// and spacing intact, to the tenant's onboarding day, where the
	// monitors' created_at already puts it. The API's recorder flushes
	// every 2s.
	time.Sleep(3 * time.Second)
	// The API may stamp with a different clock than Postgres (a Compose
	// API runs in the Docker VM), hence the earlier of both plus slack.
	since := minTime(w.now, w.startedAt).Add(-30 * time.Second)
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET occurred_at = occurred_at + ($2::timestamptz - $3::timestamptz)
		WHERE tenant_id = $1 AND actor_type = 'admin_user' AND occurred_at >= $3::timestamptz`,
		TenantID, w.start.Add(-46*time.Hour), since); err != nil {
		return fmt.Errorf("re-time setup audit rows: %w", err)
	}
	r := rand.New(rand.NewPCG(42, 7))
	type actor struct{ id, label, ip, ua string }
	actors := []actor{
		{w.users["maya.chen"], "maya.chen", "10.40.2.17", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15"},
		{w.users["lucas.martin"], "lucas.martin", "10.40.6.88", "Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0"},
		{w.users["priya.raman"], "priya.raman", "10.40.3.41", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"},
		{w.users["tom.okafor"], "tom.okafor", "10.40.9.12", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"},
		{AdminUserID, "admin", "10.40.0.5", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15"},
	}
	var monitorKeys []string
	for _, s := range w.specs {
		monitorKeys = append(monitorKeys, s.Key)
	}
	type act struct {
		action, rtype string
		weight        int
		status        int
	}
	acts := []act{
		{"auth.login", "", 30, 0}, {"monitor.update", "monitor", 22, 200}, {"monitor.run", "monitor", 10, 202}, {"monitor.test", "monitor", 5, 200},
		{"alert.acknowledge", "alert", 8, 200}, {"incident.update", "incident", 4, 200}, {"incident.timeline", "incident", 5, 201},
		{"maintenance-window.create", "maintenance-window", 3, 201}, {"status-page.update", "status-page", 3, 200}, {"monitor.create", "monitor", 4, 201},
		{"monitor.dependencies", "monitor", 2, 201}, {"notification-setting.update", "notification-setting", 1, 200}, {"sla.reports", "sla", 1, 201},
		{"alert-channel.test", "alert-channel", 2, 200}, {"monitor.update", "monitor", 2, 403},
	}
	total := 0
	for _, a := range acts {
		total += a.weight
	}
	var rows [][]any
	tid := uuid.MustParse(TenantID)
	// The live OTLP loop adds one otlp.metrics row a minute (see otlpEvery),
	// so the human activity is denser towards now: a recent band of about
	// one action every five minutes over the last four hours, then 45 days
	// skewed towards the present.
	const recent, older = 48, 420
	for i := 0; i < recent+older; i++ {
		var at time.Time
		if i < recent {
			at = w.now.Add(-2*time.Minute - time.Duration(r.Float64()*float64(4*time.Hour)))
		} else {
			ago := time.Duration(math.Pow(r.Float64(), 1.6)*float64(45*24*time.Hour)) + 10*time.Minute
			at = w.now.Add(-ago)
			if h := at.UTC().Hour(); h < 6 || h > 20 {
				at = at.Add(time.Duration(9-h%9) * time.Hour)
				if at.After(w.now) {
					at = w.now.Add(-time.Duration(r.IntN(600)) * time.Minute)
				}
			}
		}
		pick := r.IntN(total)
		var a act
		for _, x := range acts {
			if pick < x.weight {
				a = x
				break
			}
			pick -= x.weight
		}
		ac := actors[r.IntN(len(actors))]
		outcome := "success"
		if a.status == 403 {
			ac = actors[3] // the viewer tries an edit
			outcome = "denied"
		}
		var resID string
		var details any
		switch a.rtype {
		case "monitor":
			k := monitorKeys[r.IntN(len(monitorKeys))]
			resID = w.ids[k]
			if a.action == "monitor.update" && outcome == "success" {
				b, _ := json.Marshal(map[string]any{"name": w.byKey[k].Name})
				details = string(b)
			}
		case "incident", "alert", "maintenance-window", "alert-channel":
			resID = uuid.NewString()
		case "status-page":
			resID = w.pages["public"]
		case "sla":
			resID = w.slaIDs["checkout"]
		}
		var status any
		if a.status != 0 {
			status = int32(a.status)
		}
		uid := uuid.MustParse(ac.id)
		rows = append(rows, []any{at, tid, "admin_user", uid, ac.label, a.action, a.rtype, resID, outcome, status, ac.ip, ac.ua, details})
	}
	// The CI key keeps the monitor-as-code repo in sync.
	for d := 1; d <= 30; d += 2 {
		at := w.at(d, 9, 12+d)
		b, _ := json.Marshal(map[string]any{"name": "terraform-ci"})
		rows = append(rows, []any{at, tid, "api_key", nil, "terraform-ci", "monitor.update", "monitor", w.ids[monitorKeys[d%len(monitorKeys)]], "success", int32(200), "10.40.12.4", "Terraform/1.9.5 terraform-provider-probara/0.4.1", string(b)})
	}
	_, err := pool.CopyFrom(ctx, pgx.Identifier{"audit_log"}, []string{"occurred_at", "tenant_id", "actor_type", "actor_id", "actor_label", "action", "resource_type", "resource_id", "outcome", "status_code", "ip", "user_agent", "details"}, pgx.CopyFromRows(rows))
	return err
}
