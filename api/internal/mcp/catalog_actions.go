package mcp

// actionLinkNotExposed are the signed alert-action routes, deliberately left
// out of the catalog and merged into notExposed: they are authorized by a
// token from a notification, not by the caller's credential, and an agent
// acknowledges through acknowledge_alert instead.
var actionLinkNotExposed = map[string]string{
	"GET /api/v1/alert-actions/{token}":  "notification magic link, authorized by its signed token; acknowledge_alert covers it",
	"POST /api/v1/alert-actions/{token}": "notification magic link, authorized by its signed token; acknowledge_alert covers it",
}

func init() {
	for route, reason := range actionLinkNotExposed {
		notExposed[route] = reason
	}
}
