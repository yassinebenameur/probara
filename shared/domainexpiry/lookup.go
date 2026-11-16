package domainexpiry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/yassinebenameur/probara/shared/netguard"
)

// Client looks up one domain's registration. Build one per check; the
// Directory it points at is the long-lived part.
type Client struct {
	Directory *Directory
	// Dial opens every connection, RDAP HTTPS and WHOIS TCP alike. The
	// worker passes its dialGuard, so the network policy applies to each
	// hop, redirects and referrals included.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// RootCAs verifies RDAP servers; nil uses the system pool.
	RootCAs *x509.CertPool
	// WHOISRoot overrides whois.iana.org (tests).
	WHOISRoot string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// rdapShare is the part of the remaining time RDAP may use when WHOIS may
// still have to follow, so a hanging RDAP server leaves WHOIS its chance.
const rdapShare = 2.0 / 3.0

// Lookup returns what the registries say about name, the ASCII registrable
// domain (RegistrableDomain). It never fails: an undetermined expiry is an
// Info with UnknownReason set, so the caller records why.
//
// RDAP answers first. WHOIS is asked when RDAP has no service for the TLD,
// failed or was throttled, when RDAP holds the name but publishes no expiry,
// and when RDAP says the name is not registered: that answer stands only if
// WHOIS does not hold the name (a server that no longer serves the TLD, say
// after a registry change a stale bootstrap has not caught up with, would
// otherwise take every monitor in the TLD down). NotFoundConfirmed records
// that both agreed.
func (c *Client) Lookup(ctx context.Context, name string) *Info {
	info := c.lookup(ctx, name)
	info.annotate(c.now())
	return info
}

const noDateReason = "the registry publishes no expiration date"

func (c *Client) lookup(ctx context.Context, name string) *Info {
	rdapCtx, cancel := ctx, context.CancelFunc(func() {})
	if deadline, ok := ctx.Deadline(); ok {
		rdapCtx, cancel = context.WithTimeout(ctx, time.Duration(float64(time.Until(deadline))*rdapShare))
	}
	info, rdapErr := c.lookupRDAP(rdapCtx, name)
	cancel()

	if rdapErr == nil && info.ExpiresAt != "" {
		return info
	}
	if rdapErr == nil && info.IsRegistered() && PublishesNoExpiry(TLD(name)) {
		return info // WHOIS has no date either; annotate says so
	}
	whois, whoisErr := c.lookupWHOIS(ctx, name)

	switch {
	case rdapErr == nil && info.IsNotRegistered():
		if whoisErr == nil && whois.IsRegistered() {
			whois.RDAPError = "the RDAP server reported the name as not registered"
			if whois.ExpiresAt == "" {
				whois.UnknownReason = noDateReason
			}
			return whois
		}
		info.NotFoundConfirmed = whoisErr == nil && whois.IsNotRegistered()
		return info
	case rdapErr == nil:
		// Registered over RDAP without a date: WHOIS may carry one.
		if whoisErr == nil && whois.IsRegistered() && whois.ExpiresAt != "" {
			if whois.Registrar == "" {
				whois.Registrar = info.Registrar
			}
			return whois
		}
		info.UnknownReason = noDateReason
		return info
	case whoisErr == nil:
		whois.RDAPError = rdapErr.Error()
		if whois.IsRegistered() && whois.ExpiresAt == "" {
			whois.UnknownReason = noDateReason
		}
		return whois
	}
	return &Info{Domain: name, UnknownReason: unknownReason(name, rdapErr, whoisErr)}
}

// unknownReason explains a lookup no registry answered.
func unknownReason(name string, rdapErr, whoisErr error) string {
	if errors.Is(rdapErr, errNoRDAP) && errors.Is(whoisErr, errNoWHOIS) {
		return fmt.Sprintf("no RDAP or WHOIS service is known for .%s", TLD(name))
	}
	return fmt.Sprintf("rdap: %v; whois: %v", rdapErr, whoisErr)
}

// lookupError turns a connection error into text without addresses: it is
// stored in check results and quoted in notifications.
func lookupError(ctx context.Context, err error) error {
	var (
		dnsErr    *net.DNSError
		verifyErr *tls.CertificateVerificationError
	)
	switch {
	case errors.Is(err, netguard.ErrBlocked):
		return errors.New("blocked by the worker network policy")
	case errors.Is(err, errRedirectRefused):
		return errRedirectRefused
	case errors.As(err, &verifyErr):
		return errors.New("the server certificate did not verify")
	case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		return errors.New("timed out")
	case errors.As(err, &dnsErr):
		return errors.New("server name did not resolve")
	default:
		return errors.New("connection failed")
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
