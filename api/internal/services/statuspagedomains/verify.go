package statuspagedomains

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/pagedomains"
)

// Verification is DNS only. The names looked up are operator-supplied, so
// nothing here ever connects to what they resolve to: no HTTP request, no
// TLS handshake, nothing an address could be chosen for. Each lookup has its
// own timeout, the names are made absolute (a trailing dot) so a resolver's
// search list never turns them into internal names, and only a bounded part
// of each answer is read. Error text reaching the operator is fixed wording,
// never the resolver's own message (which names the resolver's address).

// Resolver is the DNS the verifier asks. *net.Resolver implements it; tests
// substitute a fake.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupCNAME(ctx context.Context, host string) (string, error)
}

// NewDNSResolver is the resolver verification uses in production: the pure
// Go resolver, so every lookup honors its context deadline.
func NewDNSResolver() Resolver {
	return &net.Resolver{PreferGo: true}
}

const (
	// lookupTimeout bounds one DNS query; a check makes at most three.
	lookupTimeout = 5 * time.Second
	// maxTXTRecords and maxTXTLength bound what is read of a TXT answer.
	maxTXTRecords = 32
	maxTXTLength  = 512
)

// checkResult is one check of a domain's two records.
type checkResult struct {
	TXTFound   bool
	CNAMEFound bool
	// Problem says what is missing or wrong, for the operator; "" when both
	// records were found.
	Problem string
}

func (c checkResult) passed() bool { return c.TXTFound && c.CNAMEFound }

// checkDomain looks up both records of hostname. target is the normalized
// STATUS_PAGE_CNAME_TARGET.
//
// The CNAME passes when the host's canonical name (the end of its CNAME
// chain) is the target, or is the target's own canonical name. The second
// form covers the usual setup where the target is itself a CNAME to a load
// balancer, and a chain through the customer's own names; it only proves the
// host routes to the platform, which is what the CNAME is for. Ownership is
// the TXT record's job.
func checkDomain(ctx context.Context, r Resolver, hostname, token, target string) checkResult {
	var res checkResult
	var problems []string

	txtName := pagedomains.VerificationRecordName(hostname)
	values, err := lookupTXT(ctx, r, txtName)
	switch {
	case err != nil:
		problems = append(problems, lookupProblem(err, "TXT record "+txtName))
	case containsToken(values, token):
		res.TXTFound = true
	case len(values) == 0:
		problems = append(problems, "TXT record "+txtName+" not found")
	default:
		problems = append(problems, "TXT record "+txtName+" does not hold this domain's verification value")
	}

	canonical, err := lookupCanonical(ctx, r, hostname)
	switch {
	case err != nil:
		problems = append(problems, lookupProblem(err, "CNAME record for "+hostname))
	case canonical == target:
		res.CNAMEFound = true
	case canonical == hostname:
		problems = append(problems, hostname+" has no CNAME record; point it at "+target)
	default:
		targetCanonical, terr := lookupCanonical(ctx, r, target)
		if terr == nil && targetCanonical == canonical {
			res.CNAMEFound = true
		} else {
			problems = append(problems, hostname+" does not resolve through "+target+" (its CNAME leads to "+canonical+")")
		}
	}

	res.Problem = strings.Join(problems, "; ")
	return res
}

func lookupTXT(ctx context.Context, r Resolver, name string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	values, err := r.LookupTXT(ctx, absolute(name))
	if isNotFound(err) {
		return nil, nil
	}
	return values, err
}

// lookupCanonical returns the end of name's CNAME chain, normalized; name
// itself when it has no CNAME.
func lookupCanonical(ctx context.Context, r Resolver, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	canonical, err := r.LookupCNAME(ctx, absolute(name))
	if err != nil {
		return "", err
	}
	return pagedomains.HostFromHeader(canonical), nil
}

func containsToken(values []string, token string) bool {
	if token == "" {
		return false
	}
	for i, v := range values {
		if i >= maxTXTRecords {
			break
		}
		if len(v) > maxTXTLength {
			continue
		}
		if strings.TrimSpace(v) == token {
			return true
		}
	}
	return false
}

func absolute(name string) string {
	if strings.HasSuffix(name, ".") {
		return name
	}
	return name + "."
}

func isNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

// lookupProblem words a failed lookup without the resolver's own message.
func lookupProblem(err error, what string) string {
	var dnsErr *net.DNSError
	switch {
	case isNotFound(err):
		return what + " not found"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &dnsErr) && dnsErr.IsTimeout:
		return what + ": DNS lookup timed out"
	default:
		return what + ": DNS lookup failed"
	}
}
