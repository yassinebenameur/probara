// Package apperr holds the sentinel errors more than one service package
// returns for the same condition. It imports nothing, so any service can
// return them and any handler can match them with errors.Is without an import
// cycle. An error only one package returns stays exported from that package.
//
// The message text reaches API clients in the response's "message" field:
// treat it as part of the API.
package apperr

import "errors"

var (
	// ErrMonitorNotFound: the monitor does not exist in the tenant.
	ErrMonitorNotFound = errors.New("monitor not found")
	// ErrAlertNotFound: the alert does not exist in the tenant.
	ErrAlertNotFound = errors.New("alert not found")
	// ErrStatusPageNotFound: the status page does not exist in the tenant.
	ErrStatusPageNotFound = errors.New("status page not found")
)
