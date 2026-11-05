package mcp

import (
	"net/http"

	contactservice "github.com/yassinebenameur/probara/api/internal/services/contacts"
	oncallservice "github.com/yassinebenameur/probara/api/internal/services/oncall"
)

// peopleTools covers the people layer: the workspace's personal SMS channel
// and on-call schedules.
func peopleTools() []spec {
	return append(peopleSettingsTools(), oncallTools()...)
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

// oncallTools are the on-call schedule tools. Schedules are informational:
// nothing pages them yet.
func oncallTools() []spec {
	schedule := map[string]string{"id": "On-call schedule ID, from list_oncall_schedules."}
	scheduleDocs := map[string]string{
		"name":        "Schedule name, unique in the workspace (1-100 characters).",
		"description": "Optional description (up to 500 characters).",
		"timezone":    "IANA time zone the handoff times are in, e.g. Europe/Paris (default UTC).",
		"rotations": "Rotation layers, lowest first; of the layers active at an instant the last one wins. Each has " +
			"kind (daily|weekly), handoff_time (HH:MM, local), handoff_weekday (0=Sunday..6, weekly only), " +
			"start_at (RFC 3339; the first member is on call from it to the first handoff after it), optional " +
			"end_at, member_ids (user IDs from list_oncall_people, in turn order) and an optional name.",
	}
	window := []param{
		{Name: "from", Description: "RFC 3339 start (default now)."},
		{Name: "to", Description: "RFC 3339 end (default 14 days after from; at most 93 days after it)."},
	}
	return []spec{
		{
			Name:  "list_oncall_schedules",
			Title: "List on-call schedules",
			Description: "List the workspace's on-call schedules with who is on call now (on_call) and next. " +
				"Schedules are informational for now: alerts do not page them yet.",
			Method: http.MethodGet, Route: "/oncall-schedules",
		},
		{
			Name:  "list_oncall_people",
			Title: "List people for on-call",
			Description: "List the users to pick from when putting someone on call in this workspace: its members, " +
				"plus superadmins already on one of its schedules (every superadmin when you are one), with their " +
				"count of verified contacts. Requires write access.",
			Method: http.MethodGet, Route: "/oncall-schedules/people", NeedsWrite: true,
		},
		{
			Name:        "get_oncall_schedule",
			Title:       "Get on-call schedule",
			Description: "Get a schedule's rotations and members, and who is on call now and next.",
			Method:      http.MethodGet, Route: "/oncall-schedules/{id}",
			PathDocs: schedule,
		},
		{
			Name:  "create_oncall_schedule",
			Title: "Create on-call schedule",
			Description: "Create an on-call schedule. Handoffs follow local wall-clock time in its time zone, so a " +
				"09:00 handoff stays at 09:00 across daylight-saving changes.",
			Method: http.MethodPost, Route: "/oncall-schedules",
			Body: typeOf[oncallservice.ScheduleInput](), Required: []string{"name", "rotations"}, FieldDocs: scheduleDocs,
		},
		{
			Name:  "update_oncall_schedule",
			Title: "Replace on-call schedule",
			Description: "Replace a schedule's name, description, time zone and rotations (rotations are replaced " +
				"as a whole: send every layer). Overrides and the calendar feed are kept.",
			Method: http.MethodPut, Route: "/oncall-schedules/{id}",
			PathDocs: schedule,
			Body:     typeOf[oncallservice.ScheduleInput](), Required: []string{"name", "rotations"}, FieldDocs: scheduleDocs,
		},
		{
			Name:        "delete_oncall_schedule",
			Title:       "Delete on-call schedule",
			Description: "Delete a schedule with its rotations, overrides and calendar feed.",
			Method:      http.MethodDelete, Route: "/oncall-schedules/{id}",
			PathDocs: schedule, Destructive: true,
		},
		{
			Name:        "get_oncall_now",
			Title:       "Who is on call",
			Description: "Who is on call on a schedule at an instant (default now), with the shift's start and end and the next shift.",
			Method:      http.MethodGet, Route: "/oncall-schedules/{id}/on-call",
			PathDocs: schedule,
			Query:    []param{{Name: "at", Description: "RFC 3339 instant (default now)."}},
		},
		{
			Name:  "preview_oncall_schedule",
			Title: "Preview on-call shifts",
			Description: "List a schedule's shifts over a window, overrides applied, adjacent shifts of the same " +
				"person merged and gaps (nobody on call) left out.",
			Method: http.MethodGet, Route: "/oncall-schedules/{id}/preview",
			PathDocs: schedule, Query: window,
		},
		{
			Name:        "list_oncall_overrides",
			Title:       "List on-call overrides",
			Description: "List a schedule's overrides that end after from (default now) and, with to, start before it.",
			Method:      http.MethodGet, Route: "/oncall-schedules/{id}/overrides",
			PathDocs: schedule,
			Query: []param{
				{Name: "from", Description: "RFC 3339; overrides ending after it (default now)."},
				{Name: "to", Description: "RFC 3339; overrides starting before it (default unbounded)."},
			},
		},
		{
			Name:  "create_oncall_override",
			Title: "Create on-call override",
			Description: "Put someone on call over a window (at most 93 days), above every rotation; of overlapping " +
				"overrides the newest wins.",
			Method: http.MethodPost, Route: "/oncall-schedules/{id}/overrides",
			PathDocs: schedule,
			Body:     typeOf[oncallservice.CreateOverrideRequest](), Required: []string{"user_id", "starts_at", "ends_at"},
			FieldDocs: map[string]string{
				"user_id":   "User to put on call, from list_oncall_people.",
				"starts_at": "RFC 3339 start.",
				"ends_at":   "RFC 3339 end, after starts_at and in the future.",
				"note":      "Optional note (up to 200 characters).",
			},
		},
		{
			Name:        "delete_oncall_override",
			Title:       "Delete on-call override",
			Description: "Remove an override; the rotation applies again over its window.",
			Method:      http.MethodDelete, Route: "/oncall-schedules/{id}/overrides/{overrideId}",
			PathDocs:    map[string]string{"id": schedule["id"], "overrideId": "Override ID, from list_oncall_overrides."},
			Destructive: true,
		},
		{
			Name:  "create_oncall_feed_link",
			Title: "Create on-call calendar link",
			Description: "Turn on the schedule's iCal feed with a new secret link, revoking any previous link. The " +
				"token is shown once; anyone with the link sees the schedule name, usernames and shifts.",
			Method: http.MethodPost, Route: "/oncall-schedules/{id}/ical-token",
			PathDocs: schedule,
		},
		{
			Name:        "disable_oncall_feed",
			Title:       "Turn off on-call calendar link",
			Description: "Turn off the schedule's iCal feed; its link stops working.",
			Method:      http.MethodDelete, Route: "/oncall-schedules/{id}/ical-token",
			PathDocs: schedule, Destructive: true,
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
	"GET /api/v1/oncall-feeds/{token}.ics":         "public iCal feed for calendar apps, authenticated by the token in the URL",
}

func init() {
	for route, reason := range peopleNotExposed {
		notExposed[route] = reason
	}
}
