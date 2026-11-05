package contacts

import (
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is a contact (or channel) that does not exist for the caller.
var ErrNotFound = errors.New("not found")

// Invalid is a validation failure shown to the caller verbatim (400).
type Invalid struct{ Message string }

func (e Invalid) Error() string { return e.Message }

func invalid(format string, args ...any) error { return Invalid{Message: fmt.Sprintf(format, args...)} }

// Conflict is a request the contact's state refuses (409).
type Conflict struct{ Message string }

func (e Conflict) Error() string { return e.Message }

// Unavailable is a transport the installation or workspace has not set up:
// no SMTP for email, no personal SMS channel for SMS (409).
type Unavailable struct{ Message string }

func (e Unavailable) Error() string { return e.Message }

// RateLimited is a send refused by the cooldown or the hourly cap (429).
type RateLimited struct {
	Message    string
	RetryAfter time.Duration
}

func (e RateLimited) Error() string { return e.Message }

// DeliveryFailed is a provider or mailer that refused the message (502).
type DeliveryFailed struct{ Err error }

func (e DeliveryFailed) Error() string { return "delivery failed: " + e.Err.Error() }
func (e DeliveryFailed) Unwrap() error { return e.Err }
