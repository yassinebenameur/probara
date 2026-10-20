//go:build unix

package worker

import "syscall"

// sipRTPSocketControl clears SO_BROADCAST on a media socket. Go sets it on
// every datagram socket it opens; with it off, the kernel refuses a write to
// a broadcast address (EACCES, counted as eperm), so a far end that names
// its subnet's broadcast address in the SDP cannot turn the probe's stream
// into one to every host on the segment. It runs after Go's own socket
// options, which is what lets it undo one.
func sipRTPSocketControl(_, _ string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 0)
	}); err != nil {
		return err
	}
	return serr
}
