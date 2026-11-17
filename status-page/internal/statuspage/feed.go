package statuspage

import (
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/pagedomains"
)

// Feed bounds. A feed reader keeps what it has already seen, so the feed
// carries only the recent tail: the most recently changed published
// incidents of the last feedIncidentWindowDays (an unresolved one always
// qualifies), each quoting its newest public updates, plus the maintenance
// the page lists.
const (
	maxFeedIncidents          = 50
	maxFeedUpdatesPerIncident = 5
	maxFeedMaintenance        = 50
	feedIncidentWindowDays    = 90
)

// statusPageFeed is everything the Atom feed renders, loaded without the
// page's monitor and analytics work: feed readers poll, and a poll must not
// cost a full page build.
type statusPageFeed struct {
	PageID      uuid.UUID
	Slug        string
	SEO         pageSEO
	PageUpdated time.Time
	Incidents   []feedIncident
	Maintenance []StatusPageMaintenanceWindow
}

// feedIncident is a published incident with the time it last changed: the
// latest of its own update, its publication and its newest public update.
type feedIncident struct {
	StatusPageIncident
	UpdatedAt time.Time
}

// GetStatusPageFeed loads a page's feed by slug; an unknown slug is
// ErrStatusPageNotFound, like the page itself. platformURL is the page's
// URL under STATUS_PAGE_BASE_URL ("" when the deployment does not configure
// one); the page's primary custom domain, when it has one, takes its place
// as the canonical URL, as on the page itself.
func (s *Service) GetStatusPageFeed(ctx context.Context, slug, platformURL string) (*statusPageFeed, error) {
	var pageID, tenantID uuid.UUID
	var title string
	var description, logoURL sql.NullString
	var settingsJSON []byte
	var updatedAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, title, description, logo_url, settings, updated_at
		FROM status_pages
		WHERE slug = $1
	`, slug).Scan(&pageID, &tenantID, &title, &description, &logoURL, &settingsJSON, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrStatusPageNotFound
		}
		return nil, fmt.Errorf("failed to get status page: %w", err)
	}

	canonicalURL := platformURL
	if host, err := s.PrimaryHost(ctx, pageID.String()); err != nil {
		return nil, err
	} else if host != "" {
		canonicalURL = pagedomains.PageURL(host)
	}

	settings := parseStatusPageSettings(settingsJSON)
	seoData := &StatusPageData{
		Slug:                slug,
		Title:               title,
		SEOTitle:            settings.SEOTitle,
		SEODescription:      settings.SEODescription,
		AllowSearchIndexing: settings.AllowSearchIndexing,
		CanonicalURL:        canonicalURL,
	}
	if description.Valid {
		seoData.Description = &description.String
	}
	if logoURL.Valid {
		seoData.LogoURL = &logoURL.String
	}

	feed := &statusPageFeed{
		PageID:      pageID,
		Slug:        slug,
		SEO:         resolvePageSEO(seoData),
		PageUpdated: updatedAt.UTC(),
	}
	if feed.Incidents, err = s.loadFeedIncidents(ctx, pageID, tenantID); err != nil {
		return nil, err
	}
	// The page's own maintenance list: occurrences in progress or starting
	// within its look-ahead (shared/maintenance), never a window's stored
	// first schedule.
	if feed.Maintenance, err = s.loadMaintenanceWindows(ctx, pageID, tenantID); err != nil {
		return nil, fmt.Errorf("failed to get maintenance windows: %w", err)
	}
	if len(feed.Maintenance) > maxFeedMaintenance {
		feed.Maintenance = feed.Maintenance[:maxFeedMaintenance]
	}
	return feed, nil
}

// loadFeedIncidents returns up to maxFeedIncidents published incidents,
// most recently changed first, that are unresolved or changed within
// feedIncidentWindowDays, each with its affected components and up to
// maxFeedUpdatesPerIncident newest public updates. Unpublished incidents are
// excluded exactly as on the page.
func (s *Service) loadFeedIncidents(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]feedIncident, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, summary, state, published_at, resolved_at, changed_at
		FROM (
			SELECT i.id, i.title, COALESCE(i.summary, '') AS summary, i.state,
				isp.published_at, i.resolved_at,
				GREATEST(i.updated_at, isp.published_at, COALESCE(lu.created_at, isp.published_at)) AS changed_at
			FROM incident_status_page_publications isp
			JOIN incidents i ON i.id = isp.incident_id AND i.tenant_id = isp.tenant_id
			LEFT JOIN LATERAL (
				SELECT e.created_at
				FROM incident_timeline_entries e
				WHERE e.tenant_id = isp.tenant_id AND e.incident_id = i.id
					AND e.entry_type = 'public_update' AND btrim(e.message) <> ''
				ORDER BY e.created_at DESC
				LIMIT 1
			) lu ON TRUE
			WHERE isp.status_page_id = $1 AND isp.tenant_id = $2 AND isp.unpublished_at IS NULL
		) recent
		WHERE state <> 'resolved' OR changed_at >= NOW() - ($3::int * INTERVAL '1 day')
		ORDER BY changed_at DESC, id DESC
		LIMIT $4
	`, statusPageID, tenantID, feedIncidentWindowDays, maxFeedIncidents)
	if err != nil {
		return nil, fmt.Errorf("query feed incidents: %w", err)
	}
	defer rows.Close()

	incidents := make([]feedIncident, 0)
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var incident feedIncident
		var id uuid.UUID
		var resolvedAt sql.NullTime
		if err := rows.Scan(&id, &incident.Title, &incident.Summary, &incident.State, &incident.PublishedAt, &resolvedAt, &incident.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan feed incident: %w", err)
		}
		incident.ID = id.String()
		if resolvedAt.Valid {
			t := resolvedAt.Time.UTC()
			incident.ResolvedAt = &t
		}
		incidents = append(incidents, incident)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feed incidents: %w", err)
	}
	if len(ids) == 0 {
		return incidents, nil
	}

	components, err := s.batchIncidentAffectedComponents(ctx, statusPageID, ids)
	if err != nil {
		return nil, err
	}
	updates, err := s.latestIncidentPublicUpdates(ctx, tenantID, ids, maxFeedUpdatesPerIncident)
	if err != nil {
		return nil, err
	}
	for i, id := range ids {
		incidents[i].AffectedComponents = components[id]
		incidents[i].Updates = updates[id]
	}
	return incidents, nil
}

// latestIncidentPublicUpdates returns up to perIncident newest public updates
// for each incident (created_at DESC, id DESC, the page's order).
func (s *Service) latestIncidentPublicUpdates(ctx context.Context, tenantID uuid.UUID, incidentIDs []uuid.UUID, perIncident int) (map[uuid.UUID][]StatusPageIncidentUpdate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ids.incident_id, e.message, e.created_at
		FROM unnest($2::uuid[]) AS ids(incident_id)
		CROSS JOIN LATERAL (
			SELECT message, created_at
			FROM incident_timeline_entries
			WHERE tenant_id = $1 AND incident_id = ids.incident_id AND entry_type = 'public_update'
				AND btrim(message) <> ''
			ORDER BY created_at DESC, id DESC
			LIMIT $3
		) e
		ORDER BY ids.incident_id, e.created_at DESC
	`, tenantID, pq.Array(incidentIDs), perIncident)
	if err != nil {
		return nil, fmt.Errorf("query feed incident updates: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID][]StatusPageIncidentUpdate, len(incidentIDs))
	for rows.Next() {
		var id uuid.UUID
		var update StatusPageIncidentUpdate
		if err := rows.Scan(&id, &update.Message, &update.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan feed incident update: %w", err)
		}
		update.Message = strings.TrimSpace(update.Message)
		result[id] = append(result[id], update)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feed incident updates: %w", err)
	}
	return result, nil
}

type atomFeed struct {
	XMLName  xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	ID       string      `xml:"id"`
	Title    atomText    `xml:"title"`
	Subtitle *atomText   `xml:"subtitle,omitempty"`
	Updated  string      `xml:"updated"`
	Author   atomPerson  `xml:"author"`
	Links    []atomLink  `xml:"link"`
	Entries  []atomEntry `xml:"entry"`
}

type atomPerson struct {
	Name string `xml:"name"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Type string `xml:"type,attr,omitempty"`
	Href string `xml:"href,attr"`
}

type atomCategory struct {
	Term string `xml:"term,attr"`
}

// atomText is a plain-text construct: readers display it literally, so
// tenant-authored text can never become markup in a reader.
type atomText struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

func plainText(s string) atomText { return atomText{Type: "text", Body: s} }

type atomEntry struct {
	ID         string         `xml:"id"`
	Title      atomText       `xml:"title"`
	Updated    string         `xml:"updated"`
	Published  string         `xml:"published,omitempty"`
	Links      []atomLink     `xml:"link"`
	Categories []atomCategory `xml:"category"`
	Content    atomText       `xml:"content"`
}

func atomTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// Atom entry ids are name-based UUIDs under the page's id: an entry keeps
// its id across polls and edits, and the same incident or occurrence shown
// on two pages (with page-specific names) is a different entry in each
// feed, as Atom requires of entries whose content differs.

// incidentEntryID is the id of a published incident in pageID's feed.
func incidentEntryID(pageID uuid.UUID, incidentID string) string {
	return "urn:uuid:" + uuid.NewSHA1(pageID, []byte("incident:"+incidentID)).String()
}

// maintenanceEntryID is the id of one listed maintenance occurrence in
// pageID's feed; each new occurrence of a repeating window is a new entry.
func maintenanceEntryID(pageID, windowID uuid.UUID, startsAt time.Time) string {
	return "urn:uuid:" + uuid.NewSHA1(pageID, []byte("maintenance:"+windowID.String()+":"+startsAt.UTC().Format(time.RFC3339))).String()
}

// renderAtomFeed renders the feed as Atom 1.0. Links are absolute under the
// page's canonical URL when known, else root-relative (Atom resolves them
// against the feed's own URL).
func renderAtomFeed(feed *statusPageFeed) ([]byte, error) {
	pageLink := feed.SEO.CanonicalURL
	if pageLink == "" {
		pageLink = "/public/status/" + feed.Slug
	}

	out := atomFeed{
		ID:      "urn:uuid:" + feed.PageID.String(),
		Title:   plainText(feed.SEO.Title),
		Author:  atomPerson{Name: feed.SEO.SiteName},
		Entries: make([]atomEntry, 0, len(feed.Incidents)+len(feed.Maintenance)),
		Links: []atomLink{
			{Rel: "self", Type: "application/atom+xml", Href: feed.SEO.FeedURL},
			{Rel: "alternate", Type: "text/html", Href: pageLink},
		},
	}
	if feed.SEO.Description != "" {
		subtitle := plainText(feed.SEO.Description)
		out.Subtitle = &subtitle
	}
	pageLinks := []atomLink{{Rel: "alternate", Type: "text/html", Href: pageLink}}

	type datedEntry struct {
		entry   atomEntry
		updated time.Time
	}
	entries := make([]datedEntry, 0, cap(out.Entries))

	for _, incident := range feed.Incidents {
		view := buildStatusPageIncidentView(incident.StatusPageIncident)
		var body strings.Builder
		fmt.Fprintf(&body, "Status: %s\n\n%s\n\n%s", view.StateLabel, view.Summary, view.AffectedComponentsText)
		if incident.ResolvedAt != nil {
			fmt.Fprintf(&body, "\nResolved: %s UTC", incident.ResolvedAt.UTC().Format("Jan 2, 2006 15:04"))
		}
		if len(incident.Updates) > 0 {
			body.WriteString("\n\nUpdates:")
			for _, u := range incident.Updates {
				fmt.Fprintf(&body, "\n- %s UTC: %s", u.CreatedAt.UTC().Format("Jan 2, 15:04"), u.Message)
			}
		}
		entries = append(entries, datedEntry{
			updated: incident.UpdatedAt,
			entry: atomEntry{
				ID:         incidentEntryID(feed.PageID, incident.ID),
				Title:      plainText(view.StateLabel + ": " + view.Title),
				Updated:    atomTime(incident.UpdatedAt),
				Published:  atomTime(incident.PublishedAt),
				Links:      pageLinks,
				Categories: []atomCategory{{Term: "incident"}, {Term: incidentCategory(view.State)}},
				Content:    plainText(body.String()),
			},
		})
	}

	for _, window := range feed.Maintenance {
		view := buildStatusPageMaintenanceView(window)
		var body strings.Builder
		fmt.Fprintf(&body, "%s: %s", view.StateLabel, view.TimeRangeText)
		if view.RepeatText != "" {
			body.WriteString("\n" + view.RepeatText)
		}
		if view.Description != "" {
			body.WriteString("\n\n" + view.Description)
		}
		body.WriteString("\n\n" + view.AffectedComponentsText)
		// The entry changes when the window is edited and when its
		// occurrence starts (Scheduled -> In progress).
		updated := window.UpdatedAt
		if window.IsActive && window.StartsAt.After(updated) {
			updated = window.StartsAt
		}
		if updated.IsZero() {
			updated = window.StartsAt
		}
		title := "Scheduled maintenance: " + view.Title
		if window.IsActive {
			title = "Maintenance in progress: " + view.Title
		}
		entries = append(entries, datedEntry{
			updated: updated,
			entry: atomEntry{
				ID:         maintenanceEntryID(feed.PageID, window.ID, window.StartsAt),
				Title:      plainText(title),
				Updated:    atomTime(updated),
				Links:      pageLinks,
				Categories: []atomCategory{{Term: "maintenance"}},
				Content:    plainText(body.String()),
			},
		})
	}

	sort.SliceStable(entries, func(i, j int) bool { return entries[i].updated.After(entries[j].updated) })
	// An empty feed is stamped with the page's own last change rather than
	// the clock, so its body and ETag stay stable between polls.
	newest := feed.PageUpdated
	for _, e := range entries {
		out.Entries = append(out.Entries, e.entry)
		if e.updated.After(newest) {
			newest = e.updated
		}
	}
	out.Updated = atomTime(newest)

	body, err := xml.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}

// incidentCategory is the entry's state category; an unknown stored state
// reads as "incident" like the page's label.
func incidentCategory(state string) string {
	switch state {
	case "investigating", "identified", "monitoring", "resolved":
		return state
	default:
		return "incident"
	}
}
