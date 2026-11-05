package mcp

import (
	"net/http"

	contactservice "github.com/yassinebenameur/probara/api/internal/services/contacts"
)

// peopleTools covers the people layer: the workspace's personal SMS channel.
func peopleTools() []spec {
	return peopleSettingsTools()
}

func peopleSettingsTools() []spec {
	return []spec{
		{
			Name:  "get_people_settings",
			Title: "Get people settings",
			Description: "Read the workspace's personal SMS channel: the SMS (Twilio) channel that texts people " +
				"directly (contact verification codes and tests). sms_channel_options lists the channels that " +
				"can fill it.",
			Method: http.MethodGet, Route: "/people-settings",
		},
		{
			Name:  "update_people_settings",
			Title: "Update people settings",
			Description: "Set or clear (null) the workspace's personal SMS channel. It must be a channel whose " +
				"plugin can text a single person (SMS via Twilio). Without one, members cannot add SMS contacts. " +
				"Tenant admins only.",
			Method: http.MethodPut, Route: "/people-settings",
			Body: typeOf[contactservice.UpdatePeopleSettingsRequest](),
			FieldDocs: map[string]string{
				"personal_sms_channel_id": "Alert channel ID from get_people_settings' sms_channel_options, or null to clear.",
			},
		},
	}
}

// peopleNotExposed are the people routes deliberately left out of the
// catalog, merged into notExposed.
var peopleNotExposed = map[string]string{
	"GET /api/v1/account/contacts":                 "personal contacts, signed-in user sessions only",
	"POST /api/v1/account/contacts":                "personal contacts, signed-in user sessions only",
	"PATCH /api/v1/account/contacts/{id}":          "personal contacts, signed-in user sessions only",
	"DELETE /api/v1/account/contacts/{id}":         "personal contacts, signed-in user sessions only",
	"POST /api/v1/account/contacts/{id}/send-code": "personal contacts, signed-in user sessions only",
	"POST /api/v1/account/contacts/{id}/verify":    "personal contacts, signed-in user sessions only",
	"POST /api/v1/account/contacts/{id}/test":      "personal contacts, signed-in user sessions only",
}

func init() {
	for route, reason := range peopleNotExposed {
		notExposed[route] = reason
	}
}
