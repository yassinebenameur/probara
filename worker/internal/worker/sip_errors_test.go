package worker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/yassinebenameur/probara/shared/netguard"
)

func TestClassifySIPError(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}
	timeoutErr := &net.OpError{Op: "read", Net: "udp", Err: os.ErrDeadlineExceeded}
	expired := &tls.CertificateVerificationError{Err: x509.CertificateInvalidError{Reason: x509.Expired}}

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"ssrf policy", &sipDialError{err: fmt.Errorf("%w: ip 10.0.0.1 not allowed", netguard.ErrBlocked)}, "ssrf_blocked"},
		{"read deadline", fmt.Errorf("failed to read response: %w", timeoutErr), "timeout"},
		{"context deadline", &sipDialError{err: context.DeadlineExceeded}, "timeout"},
		// A handshake that runs out of time is a timeout, not a TLS failure.
		{"handshake timeout", &sipDialError{handshake: true, err: timeoutErr}, "timeout"},
		{"dns", &sipDialError{err: &net.DNSError{Err: "no such host", Name: "sip.invalid", IsNotFound: true}}, "dns"},
		{"expired certificate", &sipDialError{handshake: true, err: expired}, "tls"},
		{"untrusted certificate", &sipDialError{handshake: true, err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}}, "tls"},
		{"hostname mismatch", &sipDialError{handshake: true, err: x509.HostnameError{Host: "sip.example.com", Certificate: &x509.Certificate{}}}, "tls"},
		{"record header", &sipDialError{handshake: true, err: tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}}, "tls"},
		// Any handshake failure is TLS, even one with no typed TLS cause.
		{"handshake reset", &sipDialError{handshake: true, err: errors.New("remote error: tls: handshake failure")}, "tls"},
		// Typed certificate errors are TLS even outside the handshake wrapper.
		{"bare certificate error", expired, "tls"},
		{"refused", &sipDialError{err: refused}, "refused"},
		{"dial failure", &sipDialError{err: errors.New("no resolved IPs")}, "connect"},
		{"reset mid-exchange", fmt.Errorf("failed to read response: %w", syscall.ECONNRESET), "connect"},
		{"closed mid-exchange", fmt.Errorf("failed to read response: %w", io.EOF), "connect"},
		{"malformed response", errors.New("invalid SIP response: does not start with SIP/"), "unknown"},
		// Message text no longer decides the category: the words below
		// used to read as connect, tls and timeout respectively.
		{"text only", errors.New("connect tls deadline"), "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifySIPError(tc.err); got != tc.want {
				t.Fatalf("classifySIPError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestSIPDialErrorMessage(t *testing.T) {
	cause := errors.New("boom")
	if got := (&sipDialError{err: cause}).Error(); got != "failed to connect: boom" {
		t.Fatalf("dial Error() = %q", got)
	}
	if got := (&sipDialError{handshake: true, err: cause}).Error(); got != "tls handshake: boom" {
		t.Fatalf("handshake Error() = %q", got)
	}
	if !errors.Is(&sipDialError{err: cause}, cause) {
		t.Fatal("sipDialError must unwrap to its cause")
	}
}
