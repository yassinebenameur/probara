package worker

import (
	"strings"
	"sync/atomic"
)

// sipUAS answers requests the peer sends us on a flow: a keepalive OPTIONS
// from an SBC, a BYE ending a dialog, a re-INVITE. The probe holds no server
// transactions and offers no service, so it answers statelessly and briefly,
// always back over the same flow (it never dials the Via or Contact of the
// request):
//
//	BYE matching a dialog   200
//	BYE with no match       481
//	OPTIONS, INFO           200
//	INVITE, UPDATE          488
//	CANCEL                  481
//	ACK                     no reply
//	anything else           501
//
// A request missing the headers a response must echo (Via, From, To,
// Call-ID, CSeq) cannot be answered and is dropped; one whose CSeq method
// does not match its method gets 400.
type sipUAS struct {
	send     func([]byte) error
	localTag string

	// matchDialog reports whether an in-dialog request (BYE) belongs to a
	// dialog this probe holds, and lets the owner record it. nil: no
	// dialogs, so every BYE is answered 481.
	matchDialog func(req *sipMessage) bool

	requests atomic.Int64
}

func newSIPUAS(send func([]byte) error) *sipUAS {
	return &sipUAS{send: send, localTag: sipRandomToken(4)}
}

// sipAllow lists the methods the probe understands, sent on OPTIONS and 501
// answers.
const sipAllow = "INVITE, ACK, CANCEL, BYE, OPTIONS"

func (u *sipUAS) handle(req *sipMessage) {
	u.requests.Add(1)
	method := strings.ToUpper(req.Method)
	if method == "ACK" {
		return
	}
	if len(req.Values("Via")) == 0 || req.Get("From") == "" || req.Get("To") == "" || req.Get("Call-ID") == "" {
		return
	}
	_, cseqMethod, err := req.cseq()
	if err != nil {
		return
	}
	if cseqMethod != method {
		u.reply(req, 400, "Bad Request")
		return
	}

	switch method {
	case "BYE":
		if u.matchDialog != nil && u.matchDialog(req) {
			u.reply(req, 200, "OK")
		} else {
			u.reply(req, 481, "Call/Transaction Does Not Exist")
		}
	case "OPTIONS", "INFO":
		u.reply(req, 200, "OK", sipHeader{Name: "Allow", Value: sipAllow})
	case "INVITE", "UPDATE":
		u.reply(req, 488, "Not Acceptable Here")
	case "CANCEL":
		u.reply(req, 481, "Call/Transaction Does Not Exist")
	default:
		u.reply(req, 501, "Not Implemented", sipHeader{Name: "Allow", Value: sipAllow})
	}
}

func (u *sipUAS) reply(req *sipMessage, code int, reason string, extra ...sipHeader) {
	resp := buildSIPResponse(req, code, reason, u.localTag, extra...)
	_ = u.send(resp.Bytes())
}

// buildSIPResponse answers req (RFC 3261 §8.2.6.2): every Via in order, From,
// Call-ID and CSeq copied, To copied with toTag added when it has none.
func buildSIPResponse(req *sipMessage, code int, reason, toTag string, extra ...sipHeader) *sipMessage {
	resp := &sipMessage{StatusCode: code, Reason: reason}
	for _, via := range req.Values("Via") {
		resp.Add("Via", via)
	}
	resp.Add("From", req.Get("From"))
	to := req.Get("To")
	if sipHeaderTag(to) == "" && toTag != "" {
		to += ";tag=" + toTag
	}
	resp.Add("To", to)
	resp.Add("Call-ID", req.Get("Call-ID"))
	resp.Add("CSeq", req.Get("CSeq"))
	for _, h := range extra {
		resp.Add(h.Name, h.Value)
	}
	resp.Add("Server", "Probara")
	return resp
}
