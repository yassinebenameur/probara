package models

import (
	"time"

	"github.com/google/uuid"
)

// Status-page email subscribers (api/internal/services/pagesubscribers).

// Subscriber states. An unsubscribed address is deleted, not kept in a
// third state.
const (
	StatusPageSubscriberPending   = "pending"
	StatusPageSubscriberConfirmed = "confirmed"
)

// StatusPageSubscriber is one address subscribed to a page's updates.
type StatusPageSubscriber struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	// State is pending (the confirmation link was not followed yet; nothing
	// but confirmation mail is sent) or confirmed.
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"created_at"`
	RequestedAt time.Time  `json:"requested_at"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
}

// StatusPageSubscriberCounts counts a page's subscribers by state.
type StatusPageSubscriberCounts struct {
	Confirmed int `json:"confirmed"`
	Pending   int `json:"pending"`
}

// StatusPageSubscriberListResponse is a page of a status page's subscribers
// with the page's totals and whether this installation can mail them.
type StatusPageSubscriberListResponse struct {
	Items    []StatusPageSubscriber     `json:"items"`
	Page     int                        `json:"page"`
	PageSize int                        `json:"page_size"`
	Total    int                        `json:"total"`
	Counts   StatusPageSubscriberCounts `json:"counts"`
	// Limit is the most subscribers (pending and confirmed) a page may have.
	Limit int `json:"limit"`
	// MailReady is false when the API cannot mail subscribers; MailProblems
	// says why (SMTP, APP_BASE_URL, STATUS_PAGE_BASE_URL, PROBARA_SECRETS_KEY).
	MailReady    bool     `json:"mail_ready"`
	MailProblems []string `json:"mail_problems"`
}

// StatusPageSubscribeRequest is a visitor's subscribe form, forwarded by the
// status-page service.
type StatusPageSubscribeRequest struct {
	Email string `json:"email"`
}

// StatusPageSubscribeResponse answers every accepted subscribe request the
// same way, whatever the address's state, so the endpoint never tells
// whether an address is subscribed.
type StatusPageSubscribeResponse struct {
	// State is always "check_inbox".
	State string `json:"state"`
}

// StatusPageSubscribeCheckInbox is StatusPageSubscribeResponse.State.
const StatusPageSubscribeCheckInbox = "check_inbox"

// StatusPageSubscriptionTokenRequest carries a confirmation or unsubscribe
// token from a subscriber mail.
type StatusPageSubscriptionTokenRequest struct {
	Token string `json:"token"`
}

// StatusPageSubscriptionResponse is what a confirmation or unsubscribe link
// came to.
type StatusPageSubscriptionResponse struct {
	// State is confirmed, unsubscribed, expired or invalid.
	State string `json:"state"`
	// PageTitle and PageSlug name the page for a confirmed or unsubscribed
	// link (never for an invalid one).
	PageTitle string `json:"page_title,omitempty"`
	PageSlug  string `json:"page_slug,omitempty"`
}

// StatusPageSubscriptionResponse states.
const (
	StatusPageSubscriptionConfirmed    = "confirmed"
	StatusPageSubscriptionUnsubscribed = "unsubscribed"
	StatusPageSubscriptionExpired      = "expired"
	StatusPageSubscriptionInvalid      = "invalid"
)
