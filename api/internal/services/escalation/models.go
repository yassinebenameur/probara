package escalation

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	shared "github.com/yassinebenameur/probara/shared/escalation"
)

// ErrNotFound is returned for a policy (or monitor) outside the workspace.
var ErrNotFound = errors.New("not found")

// Invalid is a validation failure shown to the caller verbatim (400).
type Invalid struct{ Message string }

func (e Invalid) Error() string { return e.Message }

func invalid(format string, args ...any) error { return Invalid{Message: fmt.Sprintf(format, args...)} }

// Conflict is a duplicate name or a policy still in use (409).
type Conflict struct{ Message string }

func (e Conflict) Error() string { return e.Message }

// Target is one recipient of a step. Exactly one ID matches Kind (none for
// all_admins). Label and ChannelType are read-only descriptions.
type Target struct {
	Kind           shared.TargetKind `json:"kind"`
	UserID         *uuid.UUID        `json:"user_id,omitempty"`
	ScheduleID     *uuid.UUID        `json:"schedule_id,omitempty"`
	ChannelID      *uuid.UUID        `json:"channel_id,omitempty"`
	NotifyAfterAck bool              `json:"notify_after_ack,omitempty"`
	Label          string            `json:"label,omitempty"`
	ChannelType    string            `json:"channel_type,omitempty"`
	// Reachable says whether this target can page anybody now: an active
	// channel, or a person target with a pageable member
	// (alertrouting.PersonTargetReachablePredicate). Read-only.
	Reachable bool `json:"reachable"`
}

// Step is one escalation step. DelaySeconds counts from when the alert
// became eligible to page, like a channel escalation tier.
type Step struct {
	DelaySeconds int      `json:"delay_seconds"`
	Targets      []Target `json:"targets"`
}

// Policy is an escalation policy as the API returns it.
type Policy struct {
	ID                 uuid.UUID `json:"id"`
	Name               string    `json:"name"`
	Description        string    `json:"description"`
	RepeatCount        int       `json:"repeat_count"`
	RepeatAfterSeconds int       `json:"repeat_after_seconds"`
	Steps              []Step    `json:"steps"`
	// MonitorCount is how many live monitors use it in notification_mode
	// 'policy'; TenantDefault whether it is the workspace default policy.
	MonitorCount  int       `json:"monitor_count"`
	TenantDefault bool      `json:"tenant_default"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// WriteRequest creates or replaces a policy.
type WriteRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	RepeatCount int    `json:"repeat_count"`
	// RepeatAfterSeconds defaults to 1800 when zero.
	RepeatAfterSeconds int    `json:"repeat_after_seconds"`
	Steps              []Step `json:"steps"`
}

// ConvertRequest builds a policy from existing channel routes: a monitor's
// custom routes (MonitorID set) or the workspace default routes.
type ConvertRequest struct {
	MonitorID *uuid.UUID `json:"monitor_id,omitempty"`
	// Name defaults to "<monitor> routes" / "Workspace default routes".
	Name string `json:"name,omitempty"`
	// Assign also switches the monitor to notification_mode 'policy' with
	// the new policy, or makes it the workspace default policy.
	Assign bool `json:"assign"`
}
