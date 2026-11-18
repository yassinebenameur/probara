package pagesubscribers

import "github.com/google/uuid"

// Every link a subscriber is sent is built here, and every page link goes
// through publicPageURL: the page link in a notice, the confirmation link
// and the unsubscribe landing. A page served on its own hostname changes
// publicPageURL alone (it has the page's ID and tenant to look the host up).
// The one-click List-Unsubscribe URL is the API's, not the page's, so it
// keeps working whatever the page's visibility or host becomes.

// pageRef identifies a page for its public links.
type pageRef struct {
	id, tenantID uuid.UUID
	slug         string
}

// publicPageURL is the page's public URL, "" when STATUS_PAGE_BASE_URL is
// unset (subscriptions cannot be turned on then).
func (s *Service) publicPageURL(p pageRef) string {
	if s.statusPageBaseURL == "" || p.slug == "" {
		return ""
	}
	return s.statusPageBaseURL + "/public/status/" + p.slug
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
	return s.publicPageURL(p) + SubscriptionPath + "?action=confirm#" + token
}

// unsubscribeLandingURL is the unsubscribe link in a notice's body. The
// status-page service serves it whatever the page's visibility.
func (s *Service) unsubscribeLandingURL(p pageRef, token string) string {
	return s.publicPageURL(p) + SubscriptionPath + "?action=unsubscribe#" + token
}

// oneClickURL is a notice's List-Unsubscribe URL.
func (s *Service) oneClickURL(token string) string {
	return s.appBaseURL + OneClickUnsubscribePath + "?token=" + token
}
