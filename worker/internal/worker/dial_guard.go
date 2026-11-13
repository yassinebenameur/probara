package worker

import (
	"errors"
	"net"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/netguard"
)

// dialGuard is a TCP dialer that enforces the worker's private-IP policy.
// Every checker that dials goes through it, whether it dials itself (TCP),
// plugs it into a transport (HTTP, gRPC, WebSocket) or hands it to a client
// library (Redis, Postgres, MongoDB). It implements DialContext (and thereby
// the Mongo driver's ContextDialer). The policy itself lives in
// shared/netguard, which notification egress uses too.
type dialGuard = netguard.Dialer

func newDialGuard(blockPrivateIPs bool, allowedCIDRs []*net.IPNet, timeout time.Duration) *dialGuard {
	return &netguard.Dialer{
		Policy:  netguard.Policy{BlockPrivate: blockPrivateIPs, AllowedCIDRs: allowedCIDRs},
		Timeout: timeout,
	}
}

// isSSRFBlocked reports whether err is the dial guard refusing a destination.
// Checkers use it to report a blocked target as "error" (a checker-policy
// problem) rather than "failure" (a statement about the target's health).
// The text fallback is deliberate: some client libraries (database drivers,
// the WebSocket handshake) flatten the dial error into a new message, so the
// netguard.ErrBlocked chain does not always survive — its "ssrf_blocked"
// prefix does.
func isSSRFBlocked(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, netguard.ErrBlocked) || strings.Contains(err.Error(), netguard.ErrBlocked.Error())
}
