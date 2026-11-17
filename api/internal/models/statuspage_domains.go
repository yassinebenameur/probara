package models

import (
	"time"

	"github.com/google/uuid"
)

// StatusPageDomain is a custom domain of a status page and what verifying it
// takes (shared/pagedomains).
type StatusPageDomain struct {
	ID           uuid.UUID `json:"id"`
	StatusPageID uuid.UUID `json:"status_page_id"`
	// Hostname in the lower-case ASCII form it is matched in (punycode for an
	// internationalized name).
	Hostname string `json:"hostname"`
	// Status is pending, verified or failed. Only a verified domain is
	// served.
	Status string `json:"status"`
	// URL is the page's address on this domain.
	URL string `json:"url"`
	// Primary marks the page's canonical domain: its oldest verified one.
	Primary bool `json:"primary"`
	// Records are the DNS records to create, each with the last check's
	// finding.
	Records []StatusPageDomainRecord `json:"records"`
	// LastError says what the last check missed; absent when it passed.
	LastError     *string    `json:"last_error,omitempty"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	VerifiedAt    *time.Time `json:"verified_at,omitempty"`
	// FailingSince is set while a verified domain fails its checks; it is
	// still served until the failure tolerance runs out.
	FailingSince *time.Time `json:"failing_since,omitempty"`
	NextCheckAt  time.Time  `json:"next_check_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

// StatusPageDomainRecord is one DNS record a domain needs.
type StatusPageDomainRecord struct {
	// Type is TXT or CNAME.
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
	// Found is the last check's finding; null before the first check.
	Found *bool `json:"found"`
}

// StatusPageDomainListResponse lists custom domains with the installation's
// CNAME target.
type StatusPageDomainListResponse struct {
	Items []StatusPageDomain `json:"items"`
	// Enabled is false when STATUS_PAGE_CNAME_TARGET is unset: no domain can
	// be added or verified.
	Enabled     bool   `json:"enabled"`
	CNAMETarget string `json:"cname_target,omitempty"`
}

// CreateStatusPageDomainRequest adds a custom domain to a status page.
type CreateStatusPageDomainRequest struct {
	StatusPageID string `json:"status_page_id"`
	Hostname     string `json:"hostname"`
}
