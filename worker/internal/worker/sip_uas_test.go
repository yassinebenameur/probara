package worker

import (
	"strings"
	"testing"
)

func testSIPServerRequest(method, cseqMethod string) *sipMessage {
	raw := strings.Join([]string{
		method + " sip:probara@192.0.2.10:40000 SIP/2.0",
		"Via: SIP/2.0/UDP 198.51.100.1:5060;branch=z9hG4bKa, SIP/2.0/UDP 198.51.100.2;branch=z9hG4bKb",
		"From: <sip:sbc@198.51.100.1>;tag=far",
		"To: <sip:probara@192.0.2.10>",
		"Call-ID: srv-1",
		"CSeq: 5 " + cseqMethod,
		"Content-Length: 0",
		"", "",
	}, "\r\n")
	msg, err := parseSIPMessage([]byte(raw))
	if err != nil {
		panic(err)
	}
	return msg
}

func TestSIPUAS_Replies(t *testing.T) {
	for _, tc := range []struct {
		name        string
		method      string
		cseqMethod  string
		inDialog    bool
		wantCode    int // 0: no reply
		wantAllowed bool
	}{
		{name: "BYE in a dialog", method: "BYE", inDialog: true, wantCode: 200},
		{name: "BYE without a dialog", method: "BYE", wantCode: 481},
		{name: "OPTIONS keepalive", method: "OPTIONS", wantCode: 200, wantAllowed: true},
		{name: "INFO", method: "INFO", wantCode: 200, wantAllowed: true},
		{name: "re-INVITE", method: "INVITE", wantCode: 488},
		{name: "UPDATE", method: "UPDATE", wantCode: 488},
		{name: "CANCEL", method: "CANCEL", wantCode: 481},
		{name: "ACK", method: "ACK", wantCode: 0},
		{name: "unknown method", method: "MESSAGE", wantCode: 501, wantAllowed: true},
		{name: "CSeq method mismatch", method: "OPTIONS", cseqMethod: "INFO", wantCode: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent [][]byte
			uas := newSIPUAS(func(b []byte) error { sent = append(sent, b); return nil })
			if tc.inDialog {
				uas.matchDialog = func(req *sipMessage) bool { return req.Get("Call-ID") == "srv-1" }
			}
			cseqMethod := tc.cseqMethod
			if cseqMethod == "" {
				cseqMethod = tc.method
			}
			req := testSIPServerRequest(tc.method, cseqMethod)
			uas.handle(req)

			if tc.wantCode == 0 {
				if len(sent) != 0 {
					t.Fatalf("sent %d replies, want none", len(sent))
				}
				return
			}
			if len(sent) != 1 {
				t.Fatalf("sent %d replies, want 1", len(sent))
			}
			resp, err := parseSIPMessage(sent[0])
			if err != nil {
				t.Fatalf("reply does not parse: %v\n%s", err, sent[0])
			}
			if resp.IsRequest || resp.StatusCode != tc.wantCode {
				t.Fatalf("reply code = %d, want %d", resp.StatusCode, tc.wantCode)
			}
			// RFC 3261 §8.2.6.2: every Via in order, From/Call-ID/CSeq
			// copied, a To tag added.
			if got, want := resp.Values("Via"), req.Values("Via"); strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("Via = %v, want %v", got, want)
			}
			for _, h := range []string{"From", "Call-ID", "CSeq"} {
				if resp.Get(h) != req.Get(h) {
					t.Errorf("%s = %q, want %q", h, resp.Get(h), req.Get(h))
				}
			}
			if sipHeaderTag(resp.Get("To")) != uas.localTag {
				t.Errorf("To = %q, want tag %q", resp.Get("To"), uas.localTag)
			}
			if got := resp.Get("Allow") != ""; got != tc.wantAllowed {
				t.Errorf("Allow present = %v, want %v", got, tc.wantAllowed)
			}
		})
	}
}

// A To that already carries a tag (an in-dialog request) is echoed as is.
func TestSIPUAS_KeepsExistingToTag(t *testing.T) {
	req := testSIPServerRequest("BYE", "BYE")
	req.Headers = append([]sipHeader(nil), req.Headers...)
	for i, h := range req.Headers {
		if h.Name == "To" {
			req.Headers[i].Value = "<sip:probara@192.0.2.10>;tag=ours"
		}
	}
	resp := buildSIPResponse(req, 200, "OK", "fresh")
	if got := resp.Get("To"); got != "<sip:probara@192.0.2.10>;tag=ours" {
		t.Fatalf("To = %q", got)
	}
}

// A request missing a header the reply must echo is dropped, not answered
// with a malformed response.
func TestSIPUAS_DropsUnanswerableRequests(t *testing.T) {
	for _, drop := range []string{"Via", "From", "To", "Call-ID", "CSeq"} {
		req := testSIPServerRequest("OPTIONS", "OPTIONS")
		var kept []sipHeader
		for _, h := range req.Headers {
			if !strings.EqualFold(h.Name, drop) {
				kept = append(kept, h)
			}
		}
		req.Headers = kept
		var sent int
		uas := newSIPUAS(func([]byte) error { sent++; return nil })
		uas.handle(req)
		if sent != 0 {
			t.Errorf("without %s: sent %d replies, want none", drop, sent)
		}
	}
}
