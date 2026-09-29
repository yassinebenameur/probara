package worker

import (
	"net"
	"time"

	"github.com/yassinebenameur/probara/shared/netguard"
)

// dialGuard is a TCP dialer that enforces the worker's private-IP policy. It
// is shared by checkers that hand connection setup to a client library
// (Redis, Postgres, MongoDB) so they get the same SSRF protection as the
// HTTP and gRPC checkers. It implements DialContext (and thereby the Mongo
// driver's ContextDialer). The policy itself lives in shared/netguard, which
// notification egress uses too.
type dialGuard = netguard.Dialer

func newDialGuard(blockPrivateIPs bool, allowedCIDRs []*net.IPNet, timeout time.Duration) *dialGuard {
	return &netguard.Dialer{
		Policy:  netguard.Policy{BlockPrivate: blockPrivateIPs, AllowedCIDRs: allowedCIDRs},
		Timeout: timeout,
	}
}
