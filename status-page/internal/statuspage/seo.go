// Head metadata for search engines and link previews: title, description,
// canonical link, Open Graph and Twitter card tags, robots, and discovery
// links for the page's feed and summary.

package statuspage

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/pageaccess"
	"github.com/yassinebenameur/probara/shared/pagedomains"
)

// maxMetaDescriptionRunes caps a description derived from the page's own
// description, which has no length limit; the seo_description setting is
// validated to the same bound by the API.
const maxMetaDescriptionRunes = 300

// Robots directives. Indexing off also asks crawlers not to follow the
// page's links: an operator who hides the page does not want it reached
// through itself either.
const (
	robotsIndex   = "index, follow"
	robotsNoIndex = "noindex, nofollow"
)

// pageSEO is what the head tags render, resolved once per build.
type pageSEO struct {
	Title        string
	Description  string
	SiteName     string
	CanonicalURL string
	OGImageURL   string
	Robots       string
	FeedURL      string
	SummaryURL   string
}

// pageIndexable reports whether crawlers may index the page. It is the one
// place that decides it, for the meta tag and the X-Robots-Tag header alike.
// Only a public page may be indexed: unlisted, password and private pages
// are noindex whatever allow_search_indexing says (an unknown or unset
// visibility is too, failing closed).
func pageIndexable(data *StatusPageData) bool {
	return data.AllowSearchIndexing && !pageaccess.NoIndex(data.Visibility)
}

// resolvePageSEO derives the head metadata. The default description is a
// fixed sentence about the page, never its current state: crawlers and link
// previews keep a snapshot for days, and "All systems operational" frozen in
// a search result during an outage is worse than no claim at all.
func resolvePageSEO(data *StatusPageData) pageSEO {
	name := strings.TrimSpace(data.Title)
	seo := pageSEO{
		Title:        strings.TrimSpace(data.SEOTitle),
		Description:  strings.TrimSpace(data.SEODescription),
		SiteName:     name,
		CanonicalURL: strings.TrimSpace(data.CanonicalURL),
		Robots:       robotsNoIndex,
	}
	if seo.Title == "" {
		seo.Title = name + " · Status"
	}
	if seo.Description == "" {
		seo.Description = truncateRunes(strings.TrimSpace(derefString(data.Description)), maxMetaDescriptionRunes)
	}
	if seo.Description == "" {
		seo.Description = fmt.Sprintf("Live status, incident history and scheduled maintenance for %s.", name)
	}
	if pageIndexable(data) {
		seo.Robots = robotsIndex
	}

	// og:image must be an absolute URL; a relative or data: logo is left out.
	if logo := strings.TrimSpace(derefString(data.LogoURL)); strings.HasPrefix(logo, "https://") || strings.HasPrefix(logo, "http://") {
		seo.OGImageURL = logo
	}

	// Discovery links are absolute under the public origin when it is known,
	// else root-relative like the page's own stream URL.
	// A custom domain's canonical URL is its root, with a trailing slash.
	base := strings.TrimRight(seo.CanonicalURL, "/")
	if base == "" {
		base = "/public/status/" + data.Slug
	}
	seo.FeedURL = base + "/" + routeFeed
	seo.SummaryURL = base + "/" + routeSummary
	return seo
}

// publicPageURL is a page's absolute URL under the deployment's public origin
// (STATUS_PAGE_BASE_URL), or "" when that origin is not configured: the head
// then carries no canonical link or og:url rather than a guessed host.
func publicPageURL(baseURL, slug string) string {
	return pagedomains.PublicURL(pagedomains.PublicBaseURL("", baseURL, slug), "")
}

// truncateRunes shortens s to at most max runes, ending in an ellipsis when
// it cut anything.
func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}
