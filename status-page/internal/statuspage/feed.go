package statuspage

import (
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/tenancy"
)

// maxFeedIncidents bounds the Atom feed to the most recently updated
// publications, and maxFeedUpdatesPerIncident the public updates quoted in
// each entry. A feed reader keeps what it has already seen, so the feed only
// needs the recent tail.
const (
	maxFeedIncidents          = 50
	maxFeedUpdatesPerIncident = 5
)

// statusPageFeed is everything the Atom feed renders, loaded without the
// page's monitor and analytics work: feed readers poll, and a poll must not
// cost a full page build.
type statusPageFeed struct {
	PageID      uuid.UUID
	Slug        string
	Title       string
	Description string
	Incidents   []feedIncident
	Maintenance []StatusPageMaintenanceWindow
}

// feedIncident is a published incident with the time it last changed:
// the latest of its own update, its publication and its newest public update.
type feedIncident struct {
	StatusPageIncident
	UpdatedAt time.Time
}

// GetStatusPageFeed loads a page's feed by slug. Unknown slugs and pages of
// a deleted workspace return ErrStatusPageNotFound, like the page itself.
func (s *Service) GetStatusPageFeed(ctx context.Context, slug string) (*statusPageFeed, error) {
	var pageID, tenantID uuid.UUID
	var feed statusPageFeed
	var description sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, slug, title, description
		FROM status_pages
		WHERE slug = $1
	`, slug).Scan(&pageID, &tenantID, &feed.Slug, &feed.Title, &description)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrStatusPageNotFound
		}
		return nil, fmt.Errorf("failed to get status page: %w", err)
	}
	if deleted, err := tenancy.IsDeleted(ctx, s.db, tenantID); err != nil {
		return nil, err
	} else if deleted {
		return nil, ErrStatusPageNotFound
	}
	feed.PageID = pageID
	feed.Description = strings.TrimSpace(description.String)

	if feed.Incidents, err = s.loadPublishedIncidentsForFeed(ctx, pageID, tenantID); err != nil {
		return nil, err
	}
	if feed.Maintenance, err = s.loadMaintenanceWindows(ctx, pageID, tenantID); err != nil {
		return nil, fmt.Errorf("failed to get maintenance windows: %w", err)
	}
	return &feed, nil
}

// loadPublishedIncidentsForFeed returns the page's maxFeedIncidents most
// recently changed published incidents, newest first, each with its affected
// components and up to maxFeedUpdatesPerIncident newest public updates.
// Every query is bounded, unlike a page's whole publication history.
func (s *Service) loadPublishedIncidentsForFeed(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]feedIncident, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.title, COALESCE(i.summary, ''), i.state, isp.published_at, i.resolved_at,
			GREATEST(i.updated_at, isp.published_at) AS changed_at
		FROM incident_status_page_publications isp
		JOIN incidents i ON i.id = isp.incident_id AND i.tenant_id = isp.tenant_id
		WHERE isp.status_page_id = $1 AND isp.tenant_id = $2 AND isp.unpublished_at IS NULL
		ORDER BY changed_at DESC, i.id DESC
		LIMIT $3
	`, statusPageID, tenantID, maxFeedIncidents)
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
		if len(incidents[i].Updates) > 0 && incidents[i].Updates[0].CreatedAt.After(incidents[i].UpdatedAt) {
			incidents[i].UpdatedAt = incidents[i].Updates[0].CreatedAt
		}
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
	Title    string      `xml:"title"`
	Subtitle string      `xml:"subtitle,omitempty"`
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

type atomText struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

type atomEntry struct {
	ID         string         `xml:"id"`
	Title      string         `xml:"title"`
	Updated    string         `xml:"updated"`
	Published  string         `xml:"published,omitempty"`
	Links      []atomLink     `xml:"link"`
	Categories []atomCategory `xml:"category"`
	Content    atomText       `xml:"content"`
}

func atomTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// renderAtomFeed renders the feed. pageURL is the page's canonical URL, or ""
// to link by path (relative references, which Atom resolves against the
// feed's own URL). now stamps an empty feed.
func renderAtomFeed(feed *statusPageFeed, pageURL string, now time.Time) ([]byte, error) {
	pageLink := pageURL
	if pageLink == "" {
		pageLink = "/public/status/" + feed.Slug
	}

	out := atomFeed{
		ID:       "urn:uuid:" + feed.PageID.String(),
		Title:    strings.TrimSpace(feed.Title) + " status",
		Subtitle: feed.Description,
		Author:   atomPerson{Name: strings.TrimSpace(feed.Title)},
		Links: []atomLink{
			{Rel: "self", Type: "application/atom+xml", Href: pageLink + "/feed.atom"},
			{Rel: "alternate", Type: "text/html", Href: pageLink},
		},
		Entries: make([]atomEntry, 0, len(feed.Incidents)+len(feed.Maintenance)),
	}

	var newest time.Time
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
		out.Entries = append(out.Entries, atomEntry{
			ID:         "urn:uuid:" + incident.ID,
			Title:      view.StateLabel + ": " + view.Title,
			Updated:    atomTime(incident.UpdatedAt),
			Published:  atomTime(incident.PublishedAt),
			Links:      []atomLink{{Rel: "alternate", Type: "text/html", Href: pageLink}},
			Categories: []atomCategory{{Term: "incident"}, {Term: view.State}},
			Content:    atomText{Type: "text", Body: body.String()},
		})
		if incident.UpdatedAt.After(newest) {
			newest = incident.UpdatedAt
		}
	}

	for _, window := range feed.Maintenance {
		view := buildStatusPageMaintenanceView(window)
		var body strings.Builder
		fmt.Fprintf(&body, "%s · %s", view.StateLabel, view.TimeRangeText)
		if view.RepeatText != "" {
			body.WriteString("\n" + view.RepeatText)
		}
		if view.Description != "" {
			body.WriteString("\n\n" + view.Description)
		}
		body.WriteString("\n\n" + view.AffectedComponentsText)
		updated := window.UpdatedAt
		if updated.IsZero() {
			updated = window.StartsAt
		}
		out.Entries = append(out.Entries, atomEntry{
			ID:         "urn:uuid:" + window.ID,
			Title:      "Maintenance: " + view.Title,
			Updated:    atomTime(updated),
			Links:      []atomLink{{Rel: "alternate", Type: "text/html", Href: pageLink}},
			Categories: []atomCategory{{Term: "maintenance"}},
			Content:    atomText{Type: "text", Body: body.String()},
		})
		if updated.After(newest) {
			newest = updated
		}
	}

	if newest.IsZero() {
		newest = now
	}
	out.Updated = atomTime(newest)

	body, err := xml.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}
