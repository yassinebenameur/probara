package mcp

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// statusPageSubscriberTools read and prune a page's email subscribers.
// Subscribing is the visitors' own act (double opt-in on the public page);
// turning subscriptions on is enable_email_subscriptions on update_status_page.
func statusPageSubscriberTools() []spec {
	page := map[string]string{"id": "Status page ID."}
	return []spec{
		{
			Name:  "get_status_page_subscriber_summary",
			Title: "Get status page subscriber summary",
			Description: "A status page's email subscribers by count (confirmed, awaiting confirmation), the per-page " +
				"limit, whether this installation can mail them (mail_ready, mail_problems), and what the latest " +
				"notices came to (sent, waiting, failed, uncertain, skipped, last error). Addresses are not included.",
			Method: http.MethodGet, Route: "/status-pages/{id}/subscribers/summary",
			PathDocs: page,
		},
		{
			Name:  "list_status_page_subscribers",
			Title: "List status page subscribers",
			Description: "A status page's email subscribers, newest first: address, state (pending until the " +
				"confirmation link is followed, then confirmed; only confirmed ones are mailed) and dates. " +
				"Requires write access because it reveals visitors' addresses.",
			Method: http.MethodGet, Route: "/status-pages/{id}/subscribers",
			PathDocs: page, Query: pageParams, NeedsWrite: true,
		},
		{
			Name:  "remove_status_page_subscriber",
			Title: "Remove status page subscriber",
			Description: "Delete one subscriber of a status page, and whatever was still waiting to be mailed to " +
				"them. The address can subscribe again (and confirm again) on the public page.",
			Method: http.MethodPost, Route: "/status-pages/{id}/subscribers/{subscriberId}/remove",
			PathDocs: map[string]string{
				"id":           "Status page ID.",
				"subscriberId": "Subscriber ID, from list_status_page_subscribers.",
			},
			Destructive: true,
		},
		{
			Name:  "remove_status_page_subscribers",
			Title: "Remove status page subscribers in bulk",
			Description: "Delete every subscriber of a status page (all: true), or every one whose address is at a " +
				"domain, e.g. after a flood of sign-ups from one domain. Returns how many were removed.",
			Method: http.MethodPost, Route: "/status-pages/{id}/subscribers/remove",
			PathDocs: page,
			Body:     typeOf[models.RemoveStatusPageSubscribersRequest](),
			FieldDocs: map[string]string{
				"all":    "Remove every subscriber of the page.",
				"domain": "Remove the subscribers whose address is at this domain (e.g. example.com).",
			},
			Destructive: true,
		},
	}
}

// statusPageSubscriberNotExposed are the CSV download and the visitor
// routes: the status-page service forwards the subscribe form, and the
// token routes are authorized by a link from a subscriber's mail, not the
// caller's credential.
var statusPageSubscriberNotExposed = map[string]string{
	"GET /api/v1/status-pages/{id}/subscribers/export":          "CSV download for the operator UI; list_status_page_subscribers returns the same rows",
	"POST /api/v1/public/status-pages/{slug}/subscribe":         "visitor subscribe form, forwarded by the status-page service",
	"POST /api/v1/public/status-page-subscriptions/confirm":     "subscription confirmation link, authorized by its signed token",
	"GET /api/v1/public/status-page-subscriptions/unsubscribe":  "subscriber mail unsubscribe link, authorized by its signed token; remove_status_page_subscriber covers it",
	"POST /api/v1/public/status-page-subscriptions/unsubscribe": "subscriber mail unsubscribe link, authorized by its signed token; remove_status_page_subscriber covers it",
}

func init() {
	for route, reason := range statusPageSubscriberNotExposed {
		notExposed[route] = reason
	}
}
