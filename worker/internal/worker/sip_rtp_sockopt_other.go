//go:build !unix

package worker

import "syscall"

// sipRTPSocketControl leaves the socket as Go opened it: the worker ships for
// Linux only, and the media vetting still refuses a local subnet's broadcast
// address (sipMediaBroadcast) on any platform.
func sipRTPSocketControl(_, _ string, _ syscall.RawConn) error { return nil }
