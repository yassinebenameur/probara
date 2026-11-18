package pagesubscribers

import (
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/pagedomains"
)

// Every link a subscriber is sent is built here, and every page link goes
// through pagedomains.PublicBaseURL, the helper the status-page service's
// canonical links use too: the page link in a notice and the confirmation
// link name the page's canonical origin, its primary custom domain when it
// has one (pagedomains.PrimaryHostSQL, read with the page), else the
// platform path. The unsubscribe landing always names the platform host: it
// must keep working after the page turns password-protected or private (the
// status-page service serves it before the gate there, never on a custom
// domain) or loses its domain. The one-click List-Unsubscribe URL is the
// API's.

// pageRef identifies a page for its public links: primaryHost is its
// canonical custom domain ("" for none).
type pageRef struct {
	id, tenantID uuid.UUID
	slug         string
	primaryHost  string
}

// publicBase is the page's canonical PublicBaseURL.
func (s *Service) publicBase(p pageRef) string {
	return pagedomains.PublicBaseURL(p.primaryHost, s.statusPageBaseURL, p.slug)
}

// publicPageURL is the page's public URL, "" when neither a custom domain
// nor STATUS_PAGE_BASE_URL is known.
func (s *Service) publicPageURL(p pageRef) string {
	return pagedomains.PublicURL(s.publicBase(p), "")
}

// SubscriptionPath is the status-page route that renders confirmation and
// unsubscribe pages (status-page/internal/statuspage/subscribe.go). Tokens
// ride in the fragment, which browsers never send to a server.
const SubscriptionPath = "/subscription"

// OneClickUnsubscribePath is the API route List-Unsubscribe names. The RFC
// 8058 one-click POST cannot set a header, so the token is a query
// parameter, which the request log never records (it logs the path only).
const OneClickUnsubscribePath = "/api/v1/public/status-page-subscriptions/unsubscribe"

// confirmURL is a confirmation mail's link.
func (s *Service) confirmURL(p pageRef, token string) string {
	return pagedomains.PublicURL(s.publicBase(p), SubscriptionPath) + "?action=confirm#" + token
}

// unsubscribeLandingURL is the unsubscribe link in a notice's body. The
// status-page service serves it whatever the page's visibility.
func (s *Service) unsubscribeLandingURL(p pageRef, token string) string {
	platform := pagedomains.PublicBaseURL("", s.statusPageBaseURL, p.slug)
	return pagedomains.PublicURL(platform, SubscriptionPath) + "?action=unsubscribe#" + token
}

// oneClickURL is a notice's List-Unsubscribe URL.
func (s *Service) oneClickURL(token string) string {
	return s.appBaseURL + OneClickUnsubscribePath + "?token=" + token
}
