package validation

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/pageaccess"
)

// maxAccessTokenLabel bounds a kiosk token label.
const maxAccessTokenLabel = 100

// validateStatusPageAccess checks the access fields of a status page create
// or update. Whether a password page actually has a password depends on the
// stored row, so the service checks that.
func validateStatusPageAccess(visibility, password *string) error {
	if visibility != nil && !pageaccess.Valid(*visibility) {
		return fmt.Errorf("visibility must be one of: %s", strings.Join(pageaccess.Visibilities, ", "))
	}
	// "" is the update's "clear the password"; the service refuses it on a
	// password page.
	if password != nil && *password != "" {
		if err := pageaccess.ValidatePassword(*password); err != nil {
			return err
		}
	}
	return nil
}

// ValidateStatusPageAccessTokenLabel checks a kiosk token label.
func ValidateStatusPageAccessTokenLabel(label string) error {
	label = strings.TrimSpace(label)
	if label == "" {
		return fmt.Errorf("label is required")
	}
	if utf8.RuneCountInString(label) > maxAccessTokenLabel {
		return fmt.Errorf("label must be at most %d characters", maxAccessTokenLabel)
	}
	return nil
}
