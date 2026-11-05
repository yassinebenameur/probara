package mcp

// chatNotExposed are the Slack app integration routes, merged into
// notExposed: Slack's OAuth redirect and signed button presses are
// provider-to-API protocol endpoints, and the install is a browser consent
// flow. An agent acknowledges through acknowledge_alert instead.
var chatNotExposed = map[string]string{
	"GET /api/v1/integrations/slack/install":       "browser OAuth consent flow (Add to Slack), signed-in tenant admin sessions only",
	"GET /api/v1/integrations/slack/callback":      "Slack OAuth redirect, authorized by its signed state",
	"POST /api/v1/integrations/slack/interactions": "Slack button presses, authorized by the X-Slack-Signature; acknowledge_alert covers it",
}

func init() {
	for route, reason := range chatNotExposed {
		notExposed[route] = reason
	}
}
