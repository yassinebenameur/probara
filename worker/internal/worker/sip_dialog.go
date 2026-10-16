package worker

import (
	"strings"
)

// sipDialog is one confirmed dialog of a test call (RFC 3261 §12): the 2xx
// that created it fixed the remote tag, the remote target (its Contact) and
// the route set (its Record-Route, reversed). A forked INVITE can create
// several; each is ACKed and BYE'd on its own.
//
// Every request of the dialog still travels over the call's one flow: the
// remote target and route set only fill the Request-URI and Route headers,
// they are never dialed (the dialed peer acts as the outbound proxy).
type sipDialog struct {
	callID    string
	localTag  string
	remoteTag string
	from      string // From name-addr, without tag
	to        string // To name-addr as the INVITE sent it, without tag

	remoteTarget string   // Contact URI of the 2xx (the INVITE's Request-URI without one)
	routeSet     []string // Record-Route values, reversed (UAC side, §12.1.2)
	strictRoute  bool     // the first route lacks ;lr (RFC 2543 strict routing)

	inviteCSeq int
	// authHeaders are the credentials the INVITE carried: the 2xx ACK must
	// repeat them (§13.2.2.4, D22). CANCEL and the non-2xx ACK carry none.
	authHeaders []sipHeader

	// ackRaw is the ACK sent for this dialog's 2xx, re-sent for every
	// retransmission of that 2xx (never a new BYE).
	ackRaw []byte

	// Set under the call's mutex.
	remoteBye bool // the callee hung up first
	byeDone   bool // a BYE was sent and settled (or abandoned)
}

// newSIPDialog creates the dialog a 2xx to invite establishes.
func newSIPDialog(invite sipRequestParams, resp *sipMessage) *sipDialog {
	d := &sipDialog{
		callID:       invite.CallID,
		localTag:     invite.FromTag,
		remoteTag:    sipHeaderTag(resp.Get("To")),
		from:         invite.From,
		to:           invite.To,
		remoteTarget: invite.RequestURI,
		inviteCSeq:   invite.CSeq,
	}
	if contact := resp.Get("Contact"); contact != "" {
		if na, err := parseSIPNameAddr(contact); err == nil && na.URI != "" {
			d.remoteTarget = na.URI
		}
	}
	rr := resp.Values("Record-Route")
	for i := len(rr) - 1; i >= 0; i-- {
		d.routeSet = append(d.routeSet, rr[i])
	}
	if len(d.routeSet) > 0 {
		d.strictRoute = !sipRouteIsLoose(d.routeSet[0])
	}
	for _, h := range invite.Extra {
		switch sipHeaderKey(h.Name) {
		case "authorization", "proxy-authorization":
			d.authHeaders = append(d.authHeaders, h)
		}
	}
	return d
}

// sipRouteIsLoose reports a route entry whose URI carries ;lr.
func sipRouteIsLoose(route string) bool {
	na, err := parseSIPNameAddr(route)
	if err != nil {
		return true
	}
	_, params, _ := strings.Cut(na.URI, ";")
	_, lr := sipParam(params, "lr")
	return lr
}

// target is the Request-URI and Route set of a request in the dialog
// (§12.2.1.1): with a loose first route (or none) the remote target and the
// route set as is; with a strict one the first route's URI is the
// Request-URI and the remote target goes last in the Route set.
func (d *sipDialog) target() (requestURI string, route []string) {
	if len(d.routeSet) == 0 || !d.strictRoute {
		return d.remoteTarget, d.routeSet
	}
	first := d.routeSet[0]
	if na, err := parseSIPNameAddr(first); err == nil {
		first = na.URI
	}
	route = append(append([]string(nil), d.routeSet[1:]...), "<"+d.remoteTarget+">")
	return first, route
}

// request builds an in-dialog request (new branch on flow).
func (d *sipDialog) request(flow *sipFlow, method string, cseq int, extra []sipHeader) *sipMessage {
	requestURI, route := d.target()
	return buildSIPRequest(sipRequestParams{
		Method:     method,
		RequestURI: requestURI,
		Via:        flow.viaValue(newSIPBranch()),
		From:       d.from,
		FromTag:    d.localTag,
		To:         d.to,
		ToTag:      d.remoteTag,
		CallID:     d.callID,
		CSeq:       cseq,
		Route:      route,
		Extra:      extra,
	})
}

// buildAck2xx is the ACK for the dialog's 2xx (§13.2.2.4): its own
// transaction-less request with a new branch, the INVITE's CSeq number, the
// dialog's target and route set, no body (the offer was in the INVITE), and
// the INVITE's credentials.
func (d *sipDialog) buildAck2xx(flow *sipFlow) *sipMessage {
	return d.request(flow, "ACK", d.inviteCSeq, d.authHeaders)
}

// buildBye ends the dialog: the next CSeq after the INVITE's, one more for
// each authenticated retry (extra carries the credentials then).
func (d *sipDialog) buildBye(flow *sipFlow, cseq int, extra []sipHeader) *sipMessage {
	return d.request(flow, "BYE", cseq, extra)
}

// matches reports a request the callee sends in this dialog: its From tag is
// our remote tag and its To tag our local tag (§12.2.2).
func (d *sipDialog) matches(req *sipMessage) bool {
	return req.Get("Call-ID") == d.callID &&
		sipHeaderTag(req.Get("From")) == d.remoteTag &&
		sipHeaderTag(req.Get("To")) == d.localTag
}

// buildSIPCancel cancels a pending INVITE (§9.1): the INVITE's Request-URI,
// top Via (same branch: it matches the INVITE's server transaction), From,
// To (no tag), Call-ID, CSeq number and Route; method CANCEL; no body, and
// no credentials (§22.1: a CANCEL cannot be challenged).
func buildSIPCancel(invite sipRequestParams) *sipMessage {
	return buildSIPRequest(sipRequestParams{
		Method:     "CANCEL",
		RequestURI: invite.RequestURI,
		Via:        invite.Via,
		From:       invite.From,
		FromTag:    invite.FromTag,
		To:         invite.To,
		CallID:     invite.CallID,
		CSeq:       invite.CSeq,
		Route:      invite.Route,
	})
}

// buildSIPNon2xxAck acknowledges a non-2xx final to an INVITE inside its
// transaction (§17.1.1.3): the INVITE's Request-URI, top Via (same branch),
// From, Call-ID, CSeq number and Route; the response's To (with its tag);
// method ACK; no body and no credentials.
func buildSIPNon2xxAck(invite sipRequestParams, resp *sipMessage) *sipMessage {
	return buildSIPRequest(sipRequestParams{
		Method:     "ACK",
		RequestURI: invite.RequestURI,
		Via:        invite.Via,
		From:       invite.From,
		FromTag:    invite.FromTag,
		To:         resp.Get("To"),
		CallID:     invite.CallID,
		CSeq:       invite.CSeq,
		Route:      invite.Route,
	})
}
