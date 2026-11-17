package mcp

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// statusPageDomainTools are the custom-domain tools of status pages.
func statusPageDomainTools() []spec {
	domain := map[string]string{"domainId": "Custom domain ID, from list_status_page_domains."}
	return []spec{
		{
			Name:  "list_status_page_domains",
			Title: "List custom domains",
			Description: "Custom domains of the workspace's status pages: hostname, status (pending, verified, failed; " +
				"only verified domains are served), the DNS records to create with what the last check found, " +
				"the last error, and the installation's CNAME target (enabled is false when custom domains are off).",
			Method: http.MethodGet, Route: "/status-page-domains",
			Query: []param{{Name: "status_page_id", Description: "Only this status page's domains."}},
		},
		{
			Name:  "add_status_page_domain",
			Title: "Add custom domain",
			Description: "Serve a status page on its own hostname, e.g. status.example.com. The domain stays pending " +
				"until both DNS records in the response exist: a CNAME from the hostname to the CNAME target and a " +
				"TXT record holding the verification value. Hostnames are unique across the installation; at most " +
				"10 per page; no wildcards or IP addresses.",
			Method: http.MethodPost, Route: "/status-page-domains",
			Body: typeOf[models.CreateStatusPageDomainRequest](), Required: []string{"status_page_id", "hostname"},
			FieldDocs: map[string]string{
				"status_page_id": "Status page ID.",
				"hostname":       "Host name to serve the page on (internationalized names are stored as punycode).",
			},
		},
		{
			Name:  "verify_status_page_domain",
			Title: "Verify custom domain",
			Description: "Check a custom domain's DNS records now instead of waiting for the next scheduled check " +
				"(refused within 30 seconds of the last one). Returns the domain with its new status.",
			Method: http.MethodPost, Route: "/status-page-domains/{domainId}/verify",
			PathDocs: domain,
		},
		{
			Name:        "remove_status_page_domain",
			Title:       "Remove custom domain",
			Description: "Remove a custom domain. The page stops being served on it at once.",
			Method:      http.MethodDelete, Route: "/status-page-domains/{domainId}",
			PathDocs: domain, Destructive: true,
		},
	}
}
