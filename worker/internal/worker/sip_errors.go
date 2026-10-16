package worker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"syscall"

	"github.com/yassinebenameur/probara/shared/netguard"
)

// sipDialError marks a failure to establish the SIP transport (dial, or the
// TLS handshake on top of it). It lets classifySIPError fall back to
// "connect" for transport setup failures without matching on message text,
// and keeps TLS handshake failures distinguishable from TCP connect errors:
// a TLS failure reads "tls handshake: …", never "failed to connect: …".
type sipDialError struct {
	handshake bool // the TCP connection succeeded; the TLS handshake failed
	err       error
	// timing is how far transport setup got (the TCP connect time when the
	// handshake failed), for the check's metrics_data.
	timing sipFlowTiming
}

func (e *sipDialError) Error() string {
	if e.handshake {
		return "tls handshake: " + e.err.Error()
	}
	return "failed to connect: " + e.err.Error()
}

func (e *sipDialError) Unwrap() error { return e.err }

// classifySIPError names the cause of a SIP transport error; Check prefixes
// the message with it ("tls: …", "refused: …"). It inspects error types, not
// message text: the old substring match read every "failed to connect: …"
// wrapper as "connect", so an expired or untrusted certificate surfaced as a
// connect error. The order matters — a handshake that times out is a
// timeout, and a refusal by the SSRF policy is ssrf_blocked even though it
// happens while dialing.
func classifySIPError(err error) string {
	var (
		netErr  net.Error
		dnsErr  *net.DNSError
		dialErr *sipDialError
		loopErr *sipLoopError
	)
	switch {
	case errors.Is(err, netguard.ErrBlocked):
		return "ssrf_blocked"
	case errors.As(err, &loopErr):
		return "loop"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.As(err, &dnsErr):
		return "dns"
	case isSIPTLSError(err):
		return "tls"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.As(err, &dialErr),
		errors.Is(err, syscall.ECONNRESET),
		errors.Is(err, syscall.EPIPE),
		errors.Is(err, io.EOF),
		errors.Is(err, io.ErrUnexpectedEOF):
		// Transport setup failed, or the peer dropped an established
		// connection mid-exchange.
		return "connect"
	default:
		// Not a transport failure, e.g. an unparseable response.
		return "unknown"
	}
}

// isSIPTLSError reports a certificate or TLS protocol failure: anything the
// handshake wrapper marks, plus the typed crypto/tls and x509 errors in case
// one surfaces outside it.
func isSIPTLSError(err error) bool {
	var (
		dialErr       *sipDialError
		verifyErr     *tls.CertificateVerificationError
		unknownAuth   x509.UnknownAuthorityError
		hostnameErr   x509.HostnameError
		invalidErr    x509.CertificateInvalidError
		alertErr      tls.AlertError
		recordErr     tls.RecordHeaderError
		echRejectErr  *tls.ECHRejectionError
		systemRootErr x509.SystemRootsError
	)
	if errors.As(err, &dialErr) && dialErr.handshake {
		return true
	}
	return errors.As(err, &verifyErr) ||
		errors.As(err, &unknownAuth) ||
		errors.As(err, &hostnameErr) ||
		errors.As(err, &invalidErr) ||
		errors.As(err, &alertErr) ||
		errors.As(err, &recordErr) ||
		errors.As(err, &echRejectErr) ||
		errors.As(err, &systemRootErr)
}
