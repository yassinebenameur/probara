package mcp

// alertActionNotExposed are the signed acknowledge-link routes, deliberately
// left out of the catalog: they are authorized by a token from a
// notification, not by the caller's credential, and an agent acknowledges
// through acknowledge_alert instead.
var alertActionNotExposed = map[string]string{
	"GET /api/v1/alert-actions/acknowledge":  "notification acknowledge link, authorized by its signed token; acknowledge_alert covers it",
	"POST /api/v1/alert-actions/acknowledge": "notification acknowledge link, authorized by its signed token; acknowledge_alert covers it",
}

func init() {
	for route, reason := range alertActionNotExposed {
		notExposed[route] = reason
	}
}
