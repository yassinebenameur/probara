package oncall

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/oncall"
)

// Limits on a schedule.
const (
	MaxRotations       = 10
	MaxMembers         = 50
	MaxNameLength      = 100
	MaxDescription     = 500
	MaxNoteLength      = 200
	MaxOverrideSpan    = 93 * 24 * time.Hour
	DefaultPreviewSpan = 14 * 24 * time.Hour
	// currentWindow is how far around now the on-call lookup looks for the
	// edges of the current shift.
	currentWindow = 35 * 24 * time.Hour
	// Feed window: two weeks back, nine weeks ahead.
	feedBack  = 14 * 24 * time.Hour
	feedAhead = 63 * 24 * time.Hour
)

// ErrNotFound is a schedule or override missing from the tenant.
var ErrNotFound = errors.New("not found")

// Invalid is a validation failure shown to the caller verbatim (400).
type Invalid struct{ Message string }

func (e Invalid) Error() string { return e.Message }

func invalid(format string, args ...any) error { return Invalid{Message: fmt.Sprintf(format, args...)} }

// Conflict is a duplicate schedule name (409).
type Conflict struct{ Message string }

func (e Conflict) Error() string { return e.Message }

// UserRef names a person in a schedule. Eligible is false once they can no
// longer be on call in this workspace (disabled, or no longer a member);
// the resolver still returns them until the schedule is edited.
type UserRef struct {
	ID               uuid.UUID `json:"id"`
	Username         string    `json:"username"`
	Eligible         bool      `json:"eligible"`
	VerifiedContacts int       `json:"verified_contacts"`
}

// Person is someone who can be put on call in the workspace.
type Person struct {
	UserRef
	// Role is the workspace role, or "superadmin".
	Role string `json:"role"`
}

// RotationInput is one rotation layer of a create or replace request.
type RotationInput struct {
	Name string              `json:"name,omitempty"`
	Kind oncall.RotationKind `json:"kind"`
	// HandoffTime is the local handoff time, "HH:MM" (24h) in the
	// schedule's time zone.
	HandoffTime string `json:"handoff_time"`
	// HandoffWeekday is 0 (Sunday) to 6 (Saturday); weekly rotations only.
	HandoffWeekday *int        `json:"handoff_weekday,omitempty"`
	StartAt        time.Time   `json:"start_at"`
	EndAt          *time.Time  `json:"end_at,omitempty"`
	MemberIDs      []uuid.UUID `json:"member_ids"`
}

// ScheduleInput creates or replaces a schedule. Rotations are replaced as a
// whole; overrides and the iCal feed are kept.
type ScheduleInput struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Timezone    string          `json:"timezone"`
	Rotations   []RotationInput `json:"rotations"`
}

// Rotation is a stored rotation layer.
type Rotation struct {
	ID             uuid.UUID           `json:"id"`
	Position       int                 `json:"position"`
	Name           string              `json:"name"`
	Kind           oncall.RotationKind `json:"kind"`
	HandoffTime    string              `json:"handoff_time"`
	HandoffWeekday *int                `json:"handoff_weekday,omitempty"`
	StartAt        time.Time           `json:"start_at"`
	EndAt          *time.Time          `json:"end_at,omitempty"`
	Members        []UserRef           `json:"members"`
}

// Shift is a stretch of time with one person on call.
type Shift struct {
	User       UserRef       `json:"user"`
	Source     oncall.Source `json:"source"`
	RotationID *uuid.UUID    `json:"rotation_id,omitempty"`
	OverrideID *uuid.UUID    `json:"override_id,omitempty"`
	Start      time.Time     `json:"start"`
	End        time.Time     `json:"end"`
}

// Schedule is a stored schedule with who is on call now.
type Schedule struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Timezone    string     `json:"timezone"`
	Rotations   []Rotation `json:"rotations"`
	ICalEnabled bool       `json:"ical_enabled"`
	// OnCall is the current shift (nil: nobody), Next the one after it.
	OnCall    *Shift    `json:"on_call"`
	Next      *Shift    `json:"next"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OnCallResponse answers who is on call at an instant.
type OnCallResponse struct {
	At     time.Time `json:"at"`
	OnCall *Shift    `json:"on_call"`
	Next   *Shift    `json:"next"`
}

// PreviewResponse lists the shifts over a window.
type PreviewResponse struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Timezone string    `json:"timezone"`
	Shifts   []Shift   `json:"shifts"`
}

// Override is a stored override.
type Override struct {
	ID        uuid.UUID `json:"id"`
	User      UserRef   `json:"user"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
	Note      string    `json:"note"`
	CreatedBy *UserRef  `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateOverrideRequest puts someone on call over [starts_at, ends_at).
type CreateOverrideRequest struct {
	UserID   uuid.UUID `json:"user_id"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Note     string    `json:"note,omitempty"`
}

// ICalToken is a freshly minted feed token, shown once.
type ICalToken struct {
	Token string `json:"token"`
	// Path is the feed's API path; URL is it on PUBLIC_BASE_URL when that is
	// configured, else empty (the client prefixes its own origin).
	Path string `json:"path"`
	URL  string `json:"url,omitempty"`
}
