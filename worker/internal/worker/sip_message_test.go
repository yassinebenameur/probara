package worker

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// crlf turns a readable multi-line literal ("\n"-separated) into wire form.
func crlf(s string) string {
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func mustParseSIP(t *testing.T, raw string) *sipMessage {
	t.Helper()
	msg, err := parseSIPMessage([]byte(raw))
	if err != nil {
		t.Fatalf("parseSIPMessage() error = %v\n%s", err, raw)
	}
	return msg
}

func TestParseSIPMessage_Response(t *testing.T) {
	msg := mustParseSIP(t, crlf(`SIP/2.0 401 Unauthorized
v: SIP/2.0/UDP 192.0.2.1:5060;branch=z9hG4bKabc;rport=5060;received=198.51.100.1
f: <sip:probe@monitor.local>;tag=1928301774
t: <sip:probe@example.com>;tag=srv
i: a84b4c76e66710@monitor.local
CSeq: 1 OPTIONS
WWW-Authenticate: Digest realm="example.com", nonce="n1", qop="auth,auth-int"
l: 0

`))
	if msg.IsRequest || msg.StatusCode != 401 || msg.Reason != "Unauthorized" {
		t.Fatalf("status line = %v %d %q", msg.IsRequest, msg.StatusCode, msg.Reason)
	}
	if got := msg.topViaBranch(); got != "z9hG4bKabc" {
		t.Fatalf("topViaBranch() = %q", got)
	}
	if got := msg.Get("Call-ID"); got != "a84b4c76e66710@monitor.local" {
		t.Fatalf("Call-ID = %q (compact i: must expand)", got)
	}
	if got := sipHeaderTag(msg.Get("To")); got != "srv" {
		t.Fatalf("To tag = %q", got)
	}
	if got := sipHeaderTag(msg.Get("From")); got != "1928301774" {
		t.Fatalf("From tag = %q", got)
	}
	n, method, err := msg.cseq()
	if err != nil || n != 1 || method != "OPTIONS" {
		t.Fatalf("cseq() = %d %q %v", n, method, err)
	}
	// The challenge keeps its commas: it is not a list header.
	if got := msg.Get("www-authenticate"); !strings.Contains(got, `qop="auth,auth-int"`) {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
	if msg.Headers[0].Name != "Via" || msg.Headers[1].Name != "From" {
		t.Fatalf("compact names must be written back in full, got %q %q", msg.Headers[0].Name, msg.Headers[1].Name)
	}
}

func TestParseSIPMessage_CompactForms(t *testing.T) {
	for compact, full := range sipCompactForms {
		if got := sipHeaderKey(compact); got != full {
			t.Errorf("sipHeaderKey(%q) = %q, want %q", compact, got, full)
		}
		if got := sipHeaderKey(strings.ToUpper(compact)); got != full {
			t.Errorf("sipHeaderKey(%q) = %q, want %q", strings.ToUpper(compact), got, full)
		}
	}
	msg := mustParseSIP(t, crlf(`SIP/2.0 200 OK
v: SIP/2.0/TCP a.example.com;branch=z9hG4bK1
m: <sip:a@192.0.2.1>
c: application/sdp
e: gzip
k: timer
s: hello
o: presence
r: <sip:b@example.com>
x: 1800
l: 3

abc`))
	for name, want := range map[string]string{
		"Contact": "<sip:a@192.0.2.1>", "Content-Type": "application/sdp", "Content-Encoding": "gzip",
		"Supported": "timer", "Subject": "hello", "Event": "presence", "Refer-To": "<sip:b@example.com>",
		"Session-Expires": "1800",
	} {
		if got := msg.Get(name); got != want {
			t.Errorf("Get(%q) = %q, want %q", name, got, want)
		}
	}
	if string(msg.Body) != "abc" {
		t.Fatalf("Body = %q", msg.Body)
	}
}

// Comma-joined list headers split into one entry per value, without
// splitting inside a quoted display name or a <URI>.
func TestParseSIPMessage_SplitsListHeaders(t *testing.T) {
	msg := mustParseSIP(t, crlf(`SIP/2.0 200 OK
Via: SIP/2.0/UDP p1.example.com;branch=z9hG4bK1, SIP/2.0/UDP p2.example.com;branch=z9hG4bK2
Via: SIP/2.0/UDP ua.example.com;branch=z9hG4bK3
Record-Route: "Proxy, Edge" <sip:p1.example.com;lr>, <sip:p2.example.com;lr;x="a,b">
Record-Route: <sip:p3.example.com;lr>
Contact: "Bob \"the, builder\"" <sip:bob@192.0.2.4?Subject=a,b>;expires=60, <sip:bob@192.0.2.5>
CSeq: 1 INVITE

`))
	vias := msg.Values("Via")
	if len(vias) != 3 || !strings.Contains(vias[0], "branch=z9hG4bK1") || !strings.Contains(vias[2], "z9hG4bK3") {
		t.Fatalf("Via = %q", vias)
	}
	if got := msg.topViaBranch(); got != "z9hG4bK1" {
		t.Fatalf("topViaBranch() = %q", got)
	}
	rr := msg.Values("Record-Route")
	want := []string{`"Proxy, Edge" <sip:p1.example.com;lr>`, `<sip:p2.example.com;lr;x="a,b">`, `<sip:p3.example.com;lr>`}
	if !reflect.DeepEqual(rr, want) {
		t.Fatalf("Record-Route = %q, want %q", rr, want)
	}
	na, err := parseSIPNameAddr(rr[0])
	if err != nil || na.Display != "Proxy, Edge" || na.URI != "sip:p1.example.com;lr" {
		t.Fatalf("parseSIPNameAddr(%q) = %+v, %v", rr[0], na, err)
	}
	contacts := msg.Values("Contact")
	if len(contacts) != 2 {
		t.Fatalf("Contact = %q", contacts)
	}
	na, err = parseSIPNameAddr(contacts[0])
	if err != nil || na.Display != `Bob "the, builder"` || na.URI != "sip:bob@192.0.2.4?Subject=a,b" {
		t.Fatalf("parseSIPNameAddr(%q) = %+v, %v", contacts[0], na, err)
	}
	if v, ok := sipParam(na.Params, "expires"); !ok || v != "60" {
		t.Fatalf("expires = %q %v", v, ok)
	}
}

func TestParseSIPMessage_UnfoldsContinuationLines(t *testing.T) {
	msg := mustParseSIP(t, crlf("OPTIONS sip:a@example.com SIP/2.0\n"+
		"Via: SIP/2.0/UDP 192.0.2.1;branch=z9hG4bKx\n"+
		"Subject: I know you're there,\n"+
		"         pick up the phone\n"+
		"\t and talk to me!\n"+
		"CSeq: 8\n"+
		"  OPTIONS\n"+
		"\n"))
	if got := msg.Get("Subject"); got != "I know you're there, pick up the phone and talk to me!" {
		t.Fatalf("Subject = %q", got)
	}
	if n, m, err := msg.cseq(); err != nil || n != 8 || m != "OPTIONS" {
		t.Fatalf("cseq() = %d %q %v", n, m, err)
	}
}

func TestParseSIPMessage_Request(t *testing.T) {
	msg := mustParseSIP(t, crlf(`BYE sip:probara@192.0.2.1:5060;transport=udp SIP/2.0
Via: SIP/2.0/UDP 198.51.100.7:5060;branch=z9hG4bKbye
From: <sip:+33123456789@trunk.example.com;user=phone>;tag=remote
To: "Probara" <sip:probara@monitor.local>;tag=local
Call-ID: call-1
CSeq: 2 BYE

`))
	if !msg.IsRequest || msg.Method != "BYE" || msg.RequestURI != "sip:probara@192.0.2.1:5060;transport=udp" {
		t.Fatalf("request line = %v %q %q", msg.IsRequest, msg.Method, msg.RequestURI)
	}
	na, err := parseSIPNameAddr(msg.Get("To"))
	if err != nil || na.Display != "Probara" || na.URI != "sip:probara@monitor.local" || na.Params != ";tag=local" {
		t.Fatalf("To = %+v, %v", na, err)
	}
}

// Bodies survive on both framings: the datagram parser honours
// Content-Length (and discards trailing bytes), the stream reader reads
// exactly Content-Length bytes and leaves the next message intact.
func TestParseSIPMessage_Bodies(t *testing.T) {
	sdp := "v=0\r\no=- 1 1 IN IP4 192.0.2.1\r\ns=-\r\nc=IN IP4 192.0.2.1\r\nt=0 0\r\nm=audio 4000 RTP/AVP 0\r\n"
	first := "SIP/2.0 200 OK\r\nVia: SIP/2.0/TCP 192.0.2.1;branch=z9hG4bK1\r\nCSeq: 1 INVITE\r\nContent-Type: application/sdp\r\nContent-Length: " +
		strconv.Itoa(len(sdp)) + "\r\n\r\n" + sdp
	second := "SIP/2.0 180 Ringing\r\nVia: SIP/2.0/TCP 192.0.2.1;branch=z9hG4bK2\r\nCSeq: 2 INVITE\r\nContent-Length: 0\r\n\r\n"

	t.Run("datagram", func(t *testing.T) {
		msg := mustParseSIP(t, first+"trailing garbage after Content-Length")
		if string(msg.Body) != sdp {
			t.Fatalf("Body = %q", msg.Body)
		}
	})

	t.Run("datagram without Content-Length", func(t *testing.T) {
		msg := mustParseSIP(t, "SIP/2.0 200 OK\r\nCSeq: 1 OPTIONS\r\n\r\nhello")
		if string(msg.Body) != "hello" {
			t.Fatalf("Body = %q", msg.Body)
		}
	})

	t.Run("stream", func(t *testing.T) {
		reader := bufio.NewReaderSize(strings.NewReader("\r\n\r\n"+first+second), sipMaxHeadBytes+1024)
		msg, err := readSIPStreamMessage(reader)
		if err != nil {
			t.Fatalf("first message: %v", err)
		}
		if string(msg.Body) != sdp || msg.StatusCode != 200 {
			t.Fatalf("first = %d %q", msg.StatusCode, msg.Body)
		}
		msg, err = readSIPStreamMessage(reader)
		if err != nil {
			t.Fatalf("second message: %v", err)
		}
		if msg.StatusCode != 180 || msg.topViaBranch() != "z9hG4bK2" || len(msg.Body) != 0 {
			t.Fatalf("second = %d %q %q", msg.StatusCode, msg.topViaBranch(), msg.Body)
		}
		if _, err := readSIPStreamMessage(reader); !errors.Is(err, io.EOF) {
			t.Fatalf("after the last message: err = %v, want io.EOF", err)
		}
	})

	t.Run("stream body cut short", func(t *testing.T) {
		reader := bufio.NewReaderSize(strings.NewReader(first[:len(first)-5]), sipMaxHeadBytes+1024)
		if _, err := readSIPStreamMessage(reader); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
		}
	})

	t.Run("stream body too large", func(t *testing.T) {
		raw := "SIP/2.0 200 OK\r\nContent-Length: 70000\r\n\r\n" + strings.Repeat("x", 70000)
		reader := bufio.NewReaderSize(strings.NewReader(raw), sipMaxHeadBytes+1024)
		if _, err := readSIPStreamMessage(reader); !errors.Is(err, errSIPMalformed) {
			t.Fatalf("err = %v, want errSIPMalformed", err)
		}
	})
}

func TestSIPMessageBytesRoundTrip(t *testing.T) {
	req := buildSIPRequest(sipRequestParams{
		Method:      "OPTIONS",
		RequestURI:  "sip:example.com",
		Via:         "SIP/2.0/UDP 192.0.2.1:40000;branch=z9hG4bKabc;rport",
		From:        "<sip:probe@monitor.local>",
		FromTag:     "ft",
		To:          "<sip:probe@example.com>",
		CallID:      "cid",
		CSeq:        3,
		Route:       []string{"<sip:p1.example.com;lr>", "<sip:p2.example.com;lr>"},
		Contact:     "<sip:probara@192.0.2.1:40000;transport=udp>",
		Extra:       []sipHeader{{Name: "accept", Value: "application/sdp"}},
		Body:        []byte("body"),
		ContentType: "text/plain",
	})
	wire := string(req.Bytes())
	for _, want := range []string{
		"OPTIONS sip:example.com SIP/2.0\r\nVia: SIP/2.0/UDP 192.0.2.1:40000;branch=z9hG4bKabc;rport\r\nMax-Forwards: 70\r\n",
		"Route: <sip:p1.example.com;lr>\r\nRoute: <sip:p2.example.com;lr>\r\n",
		"From: <sip:probe@monitor.local>;tag=ft\r\nTo: <sip:probe@example.com>\r\nCall-ID: cid\r\nCSeq: 3 OPTIONS\r\n",
		"Accept: application/sdp\r\n",
		"User-Agent: Probara\r\n",
		"Content-Type: text/plain\r\nContent-Length: 4\r\n\r\nbody",
	} {
		if !strings.Contains(wire, want) {
			t.Fatalf("wire message missing %q:\n%s", want, wire)
		}
	}
	back := mustParseSIP(t, wire)
	wantHeaders := append(append([]sipHeader(nil), req.Headers...), sipHeader{Name: "Content-Length", Value: "4"})
	if !reflect.DeepEqual(back.Headers, wantHeaders) {
		t.Fatalf("round trip headers = %q, want %q", back.Headers, wantHeaders)
	}
	if !bytes.Equal(back.Bytes(), req.Bytes()) {
		t.Fatalf("Bytes() is not stable across a parse:\n%s\n---\n%s", back.Bytes(), req.Bytes())
	}
}

func TestParseSIPNameAddr(t *testing.T) {
	tests := []struct {
		in      string
		want    sipNameAddr
		wantErr bool
	}{
		{in: "<sip:a@b>", want: sipNameAddr{URI: "sip:a@b"}},
		{in: "sip:a@b;tag=x", want: sipNameAddr{URI: "sip:a@b", Params: ";tag=x"}},
		{in: `caller<sip:caller@example.com>;tag=323`, want: sipNameAddr{Display: "caller", URI: "sip:caller@example.com", Params: ";tag=323"}},
		{in: `"A \"quoted\" name" <sips:a@b;lr> ;tag=1`, want: sipNameAddr{Display: `A "quoted" name`, URI: "sips:a@b;lr", Params: ";tag=1"}},
		{in: `"Mr. J. User <sip:j.user@example.com>`, wantErr: true}, // RFC 4475 §3.1.2.6 quotbal
		{in: `<sip:a@b`, wantErr: true},
		{in: `<>`, wantErr: true},
		{in: `<sip:a@b> junk`, wantErr: true},
	}
	for _, tt := range tests {
		got, err := parseSIPNameAddr(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseSIPNameAddr(%q) = %+v, want error", tt.in, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("parseSIPNameAddr(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
		}
	}
}

// sipTortureCase is a message adapted from the RFC 4475 torture tests:
// valid messages must parse to the asserted shape, invalid ones must be
// refused rather than half-parsed.
type sipTortureCase struct {
	name    string
	raw     string
	wantErr bool
	check   func(t *testing.T, m *sipMessage)
}

func sipTortureCorpus() []sipTortureCase {
	longValue := strings.Repeat("very", 1000)
	var manyVias strings.Builder
	for i := 0; i < 40; i++ {
		manyVias.WriteString("Via: SIP/2.0/TCP sip" + strconv.Itoa(i) + ".example.com;branch=z9hG4bK" + strconv.Itoa(i) + "\r\n")
	}
	return []sipTortureCase{
		{
			// §3.1.1.1 wsinv: whitespace before colons, folding, mixed
			// case names, compact forms.
			name: "wsinv",
			raw: crlf("INVITE sip:vivekg@chair-dnrc.example.com;unknownparam SIP/2.0\n" +
				"TO :\n sip:vivekg@chair-dnrc.example.com ;   tag    = 1918181833n\n" +
				"from   : \"J Rosenberg \\\\\\\"\"       <sip:jdrosen@example.com>\n  ;\n  tag = 98asjd8\n" +
				"MaX-fOrWaRdS: 0068\n" +
				"Call-ID: wsinv.ndaksdj@192.0.2.1\n" +
				"Content-Length   : 150\n" +
				"cseq: 0009\n  INVITE\n" +
				"Via  : SIP  /   2.0\n /UDP\n    192.0.2.2;branch=390skdjuw\n" +
				"s :\n" +
				"NewFangledHeader:   newfangled value\n continued newfangled value\n" +
				"UnknownHeaderWithUnusualValue: ;;,,;;,;\n" +
				"Content-Type: application/sdp\n" +
				"Route:\n <sip:services.example.com;lr;unknownwith=value;unknown-no-value>\n" +
				"v:  SIP  / 2.0  / TCP     spindle.example.com   ;\n  branch  =   z9hG4bK9ikj8  ,\n SIP  /    2.0   / UDP  192.168.255.111   ; branch=\n z9hG4bK30239\n" +
				"m:\"Quoted string \\\"\\\"\" <sip:jdrosen@example.com> ; newparam =\n      newvalue ;\n  secondparam ; q = 0.33\n\n" +
				strings.Repeat("x", 150)),
			check: func(t *testing.T, m *sipMessage) {
				if m.Method != "INVITE" || len(m.Body) != 150 {
					t.Fatalf("method %q body %d", m.Method, len(m.Body))
				}
				if n, method, err := m.cseq(); err != nil || n != 9 || method != "INVITE" {
					t.Fatalf("cseq() = %d %q %v", n, method, err)
				}
				if got := len(m.Values("Via")); got != 3 {
					t.Fatalf("Via count = %d, want 3", got)
				}
				if got := m.Get("Subject"); got != "" {
					t.Fatalf("empty compact s: = %q", got)
				}
				if got := m.Get("NewFangledHeader"); got != "newfangled value continued newfangled value" {
					t.Fatalf("NewFangledHeader = %q", got)
				}
			},
		},
		{
			// §3.1.1.2 intmeth: an unknown method made of every token
			// character, and a wild but legal Request-URI.
			name: "intmeth",
			raw: crlf("!interesting-Method0123456789_*+`.%indeed'~ sip:1_unusual.URI~(to-be!sure)&isn't+it$/crazy?,/;;*:&it+has=1,weird!*pas$wo~d_too.(doesn't-it)@example.com SIP/2.0\n" +
				"Via: SIP/2.0/TCP host1.example.com;branch=z9hG4bK-.!%66*_+`'~\n" +
				"To: \"BEL:\\\x07 NUL:\\\x00 DEL:\\\x7F\" <sip:1_unusual.URI~(to-be!sure)&isn't+it$/crazy?,/;;*@example.com>\n" +
				"Call-ID: intmeth.word%ZK-!.*_+'@word`~)(><:\\/\"][?}{\n" +
				"CSeq: 139122385 !interesting-Method0123456789_*+`.%indeed'~\n\n"),
			// The quoted NUL above is a raw NUL byte in the header block,
			// which this parser refuses outright (it would truncate C
			// parsers on the far end if ever echoed).
			wantErr: true,
		},
		{
			name: "intmeth without NUL",
			raw: crlf("!interesting-Method0123456789_*+`.%indeed'~ sip:1_unusual.URI~(to-be!sure)&isn't+it$/crazy?,/;;*:&it+has=1,weird!*pas$wo~d_too.(doesn't-it)@example.com SIP/2.0\n" +
				"Via: SIP/2.0/TCP host1.example.com;branch=z9hG4bK-.!%66*_+`'~\n" +
				"Call-ID: intmeth.word%ZK-!.*_+'@word`~)(><:\\/\"][?}{\n" +
				"CSeq: 139122385 !interesting-Method0123456789_*+`.%indeed'~\n\n"),
			check: func(t *testing.T, m *sipMessage) {
				if m.Method != "!interesting-Method0123456789_*+`.%indeed'~" {
					t.Fatalf("method = %q", m.Method)
				}
				if n, method, err := m.cseq(); err != nil || n != 139122385 || method != strings.ToUpper(m.Method) {
					t.Fatalf("cseq() = %d %q %v", n, method, err)
				}
				if got := m.topViaBranch(); got != "z9hG4bK-.!%66*_+`'~" {
					t.Fatalf("branch = %q", got)
				}
			},
		},
		{
			// §3.1.1.3 esc01 / §3.1.1.5 escnull: escaped characters in
			// the Request-URI (the escapes are text, not raw bytes).
			name: "escnull",
			raw: crlf("REGISTER sip:example.com SIP/2.0\nTo: sip:null-%00-null@example.com\nFrom: sip:null-%00-null@example.com;tag=839923423\n" +
				"Call-ID: escnull.39203ndfvkjdasfkq3w4otrq0adsfdfnavd\nCSeq: 14398234 REGISTER\nVia: SIP/2.0/UDP host5.example.com;branch=z9hG4bKkdjuw\nContact: <sip:%00@host5.example.com>\nContact: <sip:%00%00@host5.example.com>\nL:0\n\n"),
			check: func(t *testing.T, m *sipMessage) {
				if got := len(m.Values("Contact")); got != 2 {
					t.Fatalf("Contact count = %d", got)
				}
				if got := sipHeaderTag(m.Get("From")); got != "839923423" {
					t.Fatalf("From tag = %q", got)
				}
			},
		},
		{
			// §3.1.1.7 lwsdisp: no LWS between display name and <.
			name: "lwsdisp",
			raw:  crlf("OPTIONS sip:user@example.com SIP/2.0\nTo: sip:user@example.com\nFrom: caller<sip:caller@example.com>;tag=323\nMax-Forwards: 70\nCall-ID: lwsdisp.1234abcd@funky.example.com\nCSeq: 60 OPTIONS\nVia: SIP/2.0/UDP funky.example.com;branch=z9hG4bKkdjuw\nl: 0\n\n"),
			check: func(t *testing.T, m *sipMessage) {
				na, err := parseSIPNameAddr(m.Get("From"))
				if err != nil || na.Display != "caller" || na.URI != "sip:caller@example.com" {
					t.Fatalf("From = %+v %v", na, err)
				}
			},
		},
		{
			// §3.1.1.8 longreq: long values and many headers.
			name: "longreq",
			raw: "INVITE sip:user@example.com SIP/2.0\r\n" +
				"To: \"I have a user name of " + longValue + " proportion\" <sip:user@example.com:6000;unknownparam=" + longValue + ">\r\n" +
				"From: sip:caller@example.net;tag=" + longValue + "\r\n" +
				"Call-ID: longreq.one" + longValue + "\r\nCSeq: 3882340 INVITE\r\n" + manyVias.String() + "Content-Length: 0\r\n\r\n",
			check: func(t *testing.T, m *sipMessage) {
				if got := len(m.Values("Via")); got != 40 {
					t.Fatalf("Via count = %d", got)
				}
				if got := sipHeaderTag(m.Get("From")); got != longValue {
					t.Fatalf("long From tag lost (%d bytes)", len(got))
				}
			},
		},
		{
			// §3.1.1.9 dblreq: two requests in one datagram; the first
			// one's Content-Length ends it and the rest is discarded.
			name: "dblreq",
			raw: crlf("REGISTER sip:example.com SIP/2.0\nTo: sip:j.user@example.com\nFrom: sip:j.user@example.com;tag=43251j3j324\nMax-Forwards: 8\nI: dblreq.0ha0isndaksdj99sdfafnl3lk233412\nContact: sip:j.user@host.example.com\nCSeq: 8 REGISTER\nVia: SIP/2.0/UDP 192.0.2.125;branch=z9hG4bKkdjuw23492\nContent-Length: 0\n\n" +
				"INVITE sip:joe@example.com SIP/2.0\nt: sip:joe@example.com\nFrom: sip:caller@example.net;tag=141334\nMax-Forwards: 8\nCall-ID: dblreq.0ha0isnda977644900765@192.0.2.15\nCSeq: 8 INVITE\nVia: SIP/2.0/UDP 192.0.2.15;branch=z9hG4bKkdjuw380234\nContent-Length: 0\n\n"),
			check: func(t *testing.T, m *sipMessage) {
				if m.Method != "REGISTER" || len(m.Body) != 0 {
					t.Fatalf("method %q body %q", m.Method, m.Body)
				}
			},
		},
		{
			// §3.1.1.10 semiuri: a semicolon in the user part.
			name: "semiuri",
			raw:  crlf("OPTIONS sip:user;par=u%40example.net@example.com SIP/2.0\nTo: sip:j_user@example.com\nFrom: sip:caller@example.org;tag=33242\nMax-Forwards: 3\nCall-ID: semiuri.0ha0isndaksdj\nCSeq: 8 OPTIONS\nAccept: application/sdp, application/pkcs7-mime,\n        multipart/mixed, multipart/signed,\n        message/sip, message/sipfrag\nVia: SIP/2.0/UDP 192.0.2.1;branch=z9hG4bKkdjuw\nl: 0\n\n"),
			check: func(t *testing.T, m *sipMessage) {
				if m.RequestURI != "sip:user;par=u%40example.net@example.com" {
					t.Fatalf("Request-URI = %q", m.RequestURI)
				}
				if got := m.Get("Accept"); !strings.HasSuffix(got, "message/sip, message/sipfrag") {
					t.Fatalf("Accept = %q", got)
				}
			},
		},
		{
			// §3.1.1.11 transports: unusual transports in Via.
			name: "transports",
			raw:  crlf("OPTIONS sip:user@example.com SIP/2.0\nTo: sip:user@example.com\nFrom: <sip:caller@example.com>;tag=323\nMax-Forwards: 70\nCall-ID:  transports.kijh4akdnaqjkwendsasfdj\nAccept: application/sdp\nCSeq: 60 OPTIONS\nVia: SIP/2.0/UDP t1.example.com;branch=z9hG4bKkdjuw\nVia: SIP/2.0/SCTP t2.example.com;branch=z9hG4bKklasjdhf\nVia: SIP/2.0/TLS t3.example.com;branch=z9hG4bK2980unddj\nVia: SIP/2.0/UNKNOWN t4.example.com;branch=z9hG4bKasd0f3en\nVia: SIP/2.0/TCP t5.example.com;branch=z9hG4bK0a9idfnee\nl: 0\n\n"),
			check: func(t *testing.T, m *sipMessage) {
				if got := len(m.Values("Via")); got != 5 {
					t.Fatalf("Via count = %d", got)
				}
			},
		},
		{
			// §3.1.1.12 mpart01: a multipart body, kept byte for byte.
			name: "mpart01",
			raw: "MESSAGE sip:kumiko@example.org SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1:5070;branch=z9hG4bK-d87543-4dade06d0bdb11ee-1--d87543-;rport\r\nMax-Forwards: 70\r\nRoute: <sip:127.0.0.1:5080>\r\nIdentity: r5mwreLuyDRYBi/0TiPwEsY3rEVsk/G2WxhgTV1PF7hHuLIK0YWVKZhKv9Mj8UeXqkMVbnVq37CD+813gvYjcBUaZngQmXc9WNZSDNGCzA+fWl9MEUHWIZo1CeJebdY/XlgKeTa0Olvq0rt70Q5jiSfbqMJmQFteeivUhkMWYUA=\r\nContact: <sip:fluffy@127.0.0.1:5070>\r\nTo: <sip:kumiko@example.org>\r\nFrom: <sip:fluffy@example.com>;tag=2fb0dcc9\r\nCall-ID: 3d9485ad0c49859b@Zmx1ZmZ5LW1hYy0xNi5sb2NhbA..\r\nCSeq: 1 MESSAGE\r\nContent-Transfer-Encoding: binary\r\nContent-Type: multipart/mixed;boundary=7a9cbec02ceef655\r\nDate: Sat, 15 Oct 2005 04:44:56 GMT\r\nUser-Agent: SIPimp.org/0.2.5 (curses)\r\nContent-Length: 87\r\n\r\n" +
				"--7a9cbec02ceef655\r\nContent-Type: text/plain\r\n\r\nHello\r\n--7a9cbec02ceef655--\r\n" + strings.Repeat(" ", 87-len("--7a9cbec02ceef655\r\nContent-Type: text/plain\r\n\r\nHello\r\n--7a9cbec02ceef655--\r\n")),
			check: func(t *testing.T, m *sipMessage) {
				if len(m.Body) != 87 || !bytes.HasPrefix(m.Body, []byte("--7a9cbec02ceef655")) {
					t.Fatalf("body = %q", m.Body)
				}
			},
		},
		{
			// §3.1.1.13 unreason: a reason phrase in UTF-8 with odd
			// characters.
			name: "unreason",
			raw:  crlf("SIP/2.0 200 = 2**3 * 5**2 но сто девяносто девять - простое\nVia: SIP/2.0/UDP 192.0.2.198;branch=z9hG4bK1324923\nCall-ID: unreason.1234ksdfak3j2erwedfsASdf\nCSeq: 35 INVITE\nFrom: sip:user@example.com;tag=11141343\nTo: sip:user@example.edu;tag=2229\nContent-Length: 0\n\n"),
			check: func(t *testing.T, m *sipMessage) {
				if m.StatusCode != 200 || !strings.HasSuffix(m.Reason, "простое") {
					t.Fatalf("status = %d %q", m.StatusCode, m.Reason)
				}
			},
		},
		{
			// §3.1.1.14 noreason: an empty reason phrase.
			name: "noreason",
			raw:  crlf("SIP/2.0 100 \nVia: SIP/2.0/UDP 192.0.2.105;branch=z9hG4bK2398ndaoe\nCall-ID: noreason.asndj203insdf99223ndf\nCSeq: 35 INVITE\nFrom: <sip:user@example.com>;tag=39ansfi3\nTo: <sip:user@example.edu>;tag=902jndnke3\nContent-Length: 0\n\n"),
			check: func(t *testing.T, m *sipMessage) {
				if m.StatusCode != 100 || m.Reason != "" {
					t.Fatalf("status = %d %q", m.StatusCode, m.Reason)
				}
			},
		},

		// Invalid messages.
		{
			// §3.1.2.2 clerr: Content-Length larger than the datagram's body.
			name:    "clerr",
			raw:     crlf("INVITE sip:user@example.com SIP/2.0\nMax-Forwards: 80\nTo: sip:j.user@example.com\nFrom: sip:caller@example.net;tag=93942939o2\nContact: <sip:caller@hungry.example.net>\nCall-ID: clerr.0ha0isndaksdjweiafasdk3\nCSeq: 8 INVITE\nVia: SIP/2.0/UDP host5.example.com;branch=z9hG4bK-39234-23523\nContent-Type: application/sdp\nContent-Length: 9999\n\nv=0\n"),
			wantErr: true,
		},
		{
			// §3.1.2.3 scalar02: a Content-Length too large to be a number.
			name:    "scalar02",
			raw:     crlf("REGISTER sip:example.com SIP/2.0\nVia: SIP/2.0/TCP host129.example.com;branch=z9hG4bK342sdfoi3\nTo: <sip:user@example.com>\nFrom: <sip:user@example.com>;tag=239232jh3\nCSeq: 36 REGISTER\nCall-ID: scalar02.23o0pd9vanlq3wnrlnewofjas9ui32\nMax-Forwards: 300\nExpires: 1\nContent-Length: 999999999999999999999999999999999999999999999999\n\n"),
			wantErr: true,
		},
		{
			// §3.1.2.4 scalarlg / ncl: a negative Content-Length.
			name:    "ncl",
			raw:     crlf("INVITE sip:user@example.com SIP/2.0\nMax-Forwards: 254\nTo: sip:j.user@example.com\nFrom: sip:caller@example.net;tag=32394234\nCall-ID: ncl.0ha0isndaksdj2193423r542w35\nCSeq: 0 INVITE\nVia: SIP/2.0/UDP 192.0.2.53;branch=z9hG4bKkdjuw\nContent-Type: application/sdp\nContent-Length: -999\n\nv=0\n"),
			wantErr: true,
		},
		{
			name:    "conflicting Content-Length",
			raw:     crlf("SIP/2.0 200 OK\nContent-Length: 0\nl: 4\n\nabcd"),
			wantErr: true,
		},
		{
			// §3.1.2.7 ltgtruri: Request-URI in angle brackets.
			name:    "ltgtruri",
			raw:     crlf("INVITE <sip:user@example.com> SIP/2.0\nTo: sip:user@example.com\nFrom: sip:caller@example.net;tag=39291\nMax-Forwards: 23\nCall-ID: ltgtruri.1@192.0.2.5\nCSeq: 1 INVITE\nVia: SIP/2.0/UDP 192.0.2.5\nl: 0\n\n"),
			wantErr: true,
		},
		{
			// §3.1.2.8 lwsruri: whitespace inside the Request-URI.
			name:    "lwsruri",
			raw:     crlf("INVITE sip:user@example.com; lr SIP/2.0\nTo: sip:user@example.com;tag=3xfe-9921883-z9f\nFrom: sip:caller@example.net;tag=231413434\nMax-Forwards: 5\nCall-ID: lwsruri.asdfasdoeoi2323-asdfwrn23-asd834rk423\nCSeq: 2130706432 INVITE\nVia: SIP/2.0/UDP 192.0.2.1:5060;branch=z9hG4bKkdjuw2395\nl: 0\n\n"),
			wantErr: true,
		},
		{
			// §3.1.2.9 lwsstart: several spaces between elements.
			name:    "lwsstart",
			raw:     crlf("INVITE  sip:user@example.com  SIP/2.0\nMax-Forwards: 8\nTo: sip:user@example.com\nFrom: sip:caller@example.net;tag=8814\nCall-ID: lwsstart.dfknq234oi243099adsdfnawe3@example.com\nCSeq: 1893884 INVITE\nVia: SIP/2.0/UDP host1.example.com;branch=z9hG4bKkdjuw3923\nl: 0\n\n"),
			wantErr: true,
		},
		{
			// §3.1.2.10 trws: trailing whitespace after the version.
			name:    "trws",
			raw:     "OPTIONS sip:remote-target@example.com SIP/2.0  \r\nVia: SIP/2.0/TCP host1.example.com;branch=z9hG4bK299342093\r\nCSeq: 39234321 OPTIONS\r\nl: 0\r\n\r\n",
			wantErr: true,
		},
		{
			// §3.1.2.15 badvers: an unknown protocol version.
			name:    "badvers",
			raw:     crlf("OPTIONS sip:t.watson@example.org SIP/7.0\nVia:     SIP/7.0/UDP c.example.com;branch=z9hG4bKkdjuw\nMax-Forwards:     70\nFrom:    A. Bell <sip:a.g.bell@example.com>;tag=qweoiqpe\nTo:      T. Watson <sip:t.watson@example.org>\nCall-ID: badvers.31417@c.example.com\nCSeq:    1 OPTIONS\nl: 0\n\n"),
			wantErr: true,
		},
		{
			name:    "response with a bad version",
			raw:     crlf("SIP/3.0 200 OK\nCSeq: 1 OPTIONS\n\n"),
			wantErr: true,
		},
		{
			name:    "status code out of range",
			raw:     crlf("SIP/2.0 099 Odd\nCSeq: 1 OPTIONS\n\n"),
			wantErr: true,
		},
		{
			name:    "four-digit status code",
			raw:     crlf("SIP/2.0 2000 OK\nCSeq: 1 OPTIONS\n\n"),
			wantErr: true,
		},
		{
			name:    "header without a colon",
			raw:     crlf("SIP/2.0 200 OK\nCSeq 1 OPTIONS\n\n"),
			wantErr: true,
		},
		{
			name:    "space inside a header name",
			raw:     crlf("SIP/2.0 200 OK\nCall ID: x\n\n"),
			wantErr: true,
		},
		{
			name:    "continuation before any header",
			raw:     crlf("SIP/2.0 200 OK\n folded\n\n"),
			wantErr: true,
		},
		{
			name:    "bare CR inside a header",
			raw:     "SIP/2.0 200 OK\r\nX-A: a\rX-B: b\r\n\r\n",
			wantErr: true,
		},
		{
			name:    "empty",
			raw:     "\r\n\r\n",
			wantErr: true,
		},
	}
}

func TestParseSIPMessage_TortureCorpus(t *testing.T) {
	for _, tc := range sipTortureCorpus() {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := parseSIPMessage([]byte(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parsed an invalid message: %+v", msg)
				}
				if !errors.Is(err, errSIPMalformed) {
					t.Fatalf("err = %v, want errSIPMalformed", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSIPMessage() error = %v", err)
			}
			if tc.check != nil {
				tc.check(t, msg)
			}
			// Whatever parses must survive its own serialization.
			again, err := parseSIPMessage(msg.Bytes())
			if err != nil {
				t.Fatalf("re-parse of Bytes() failed: %v\n%s", err, msg.Bytes())
			}
			if !bytes.Equal(again.Bytes(), msg.Bytes()) {
				t.Fatalf("Bytes() not stable:\n%s\n---\n%s", msg.Bytes(), again.Bytes())
			}
		})
	}
}

// FuzzParseSIPMessage: the parser never panics, and anything it accepts
// serializes to a message that parses back to the same thing on both
// framings.
func FuzzParseSIPMessage(f *testing.F) {
	for _, tc := range sipTortureCorpus() {
		f.Add([]byte(tc.raw))
	}
	f.Add([]byte("SIP/2.0 200 OK\r\nVia: SIP/2.0/UDP a;branch=z9hG4bK1\r\nCSeq: 1 OPTIONS\r\nContent-Length: 0\r\n\r\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		msg, err := parseSIPMessage(data)
		// The stream reader must not panic on arbitrary input either.
		_, _ = readSIPStreamMessage(bufio.NewReaderSize(bytes.NewReader(data), sipMaxHeadBytes+1024))
		if err != nil {
			return
		}
		_ = msg.topViaBranch()
		_, _, _ = msg.cseq()
		for _, h := range msg.Headers {
			_, _ = parseSIPNameAddr(h.Value)
			_ = sipHeaderTag(h.Value)
		}

		wire := msg.Bytes()
		again, err := parseSIPMessage(wire)
		if err != nil {
			t.Fatalf("re-parse failed: %v\ninput %q\nwire %q", err, data, wire)
		}
		if !bytes.Equal(again.Bytes(), wire) {
			t.Fatalf("not stable:\n%q\n%q", wire, again.Bytes())
		}
		if len(wire) > sipMaxHeadBytes {
			// Serialization can expand a head that only just fit (compact
			// names, split lists); the stream cap is not the property here.
			return
		}
		streamed, err := readSIPStreamMessage(bufio.NewReaderSize(bytes.NewReader(wire), sipMaxHeadBytes+1024))
		if err != nil {
			t.Fatalf("stream read of serialized message failed: %v\n%q", err, wire)
		}
		if !bytes.Equal(streamed.Bytes(), wire) {
			t.Fatalf("stream and datagram parses differ:\n%q\n%q", wire, streamed.Bytes())
		}
	})
}
