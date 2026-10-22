package worker

import (
	"strings"
	"testing"
)

func TestParseSIPDigestChallenge(t *testing.T) {
	t.Run("parses quoted params with commas and qop list", func(t *testing.T) {
		header := `Digest realm="sip.example.com", nonce="abc,123", opaque="xyz", algorithm=MD5, qop="auth,auth-int"`
		ch, err := parseSIPDigestChallenge(header)
		if err != nil {
			t.Fatalf("parseSIPDigestChallenge() error = %v", err)
		}
		if ch.Realm != "sip.example.com" || ch.Nonce != "abc,123" || ch.Opaque != "xyz" || ch.Algorithm != "MD5" {
			t.Fatalf("unexpected challenge: %+v", ch)
		}
		if len(ch.QOP) != 2 || ch.QOP[0] != "auth" || ch.QOP[1] != "auth-int" {
			t.Fatalf("unexpected qop: %+v", ch.QOP)
		}
	})

	t.Run("rejects non-digest schemes", func(t *testing.T) {
		if _, err := parseSIPDigestChallenge(`Basic realm="x"`); err == nil {
			t.Fatal("expected error for Basic scheme")
		}
	})

	t.Run("rejects missing nonce", func(t *testing.T) {
		if _, err := parseSIPDigestChallenge(`Digest realm="x"`); err == nil {
			t.Fatal("expected error for missing nonce")
		}
	})
}

// RFC 2617 §3.5 example: MD5 with qop=auth.
func TestBuildSIPDigestAuthorization_MD5RFCVector(t *testing.T) {
	challenge := &sipDigestChallenge{
		Realm:  "testrealm@host.com",
		Nonce:  "dcd98b7102dd2f0e8b11d0f600bfb0c093",
		Opaque: "5ccc069c403ebaf9f0171e9517f40e41",
		QOP:    []string{"auth", "auth-int"},
	}
	header, err := buildSIPDigestAuthorizationWithCnonce(
		challenge, "Mufasa", "Circle Of Life", "GET", "/dir/index.html", "0a4f113b", 1,
	)
	if err != nil {
		t.Fatalf("buildSIPDigestAuthorizationWithCnonce() error = %v", err)
	}
	if !strings.Contains(header, `response="6629fae49393a05397450978507c4ef1"`) {
		t.Fatalf("response does not match RFC 2617 vector: %s", header)
	}
	for _, want := range []string{`username="Mufasa"`, `qop=auth`, `nc=00000001`, `cnonce="0a4f113b"`, `opaque="5ccc069c403ebaf9f0171e9517f40e41"`, "algorithm=MD5"} {
		if !strings.Contains(header, want) {
			t.Fatalf("header missing %q: %s", want, header)
		}
	}
}

// RFC 7616 §3.9.1 example: SHA-256 with qop=auth.
func TestBuildSIPDigestAuthorization_SHA256RFCVector(t *testing.T) {
	challenge := &sipDigestChallenge{
		Realm:     "http-auth@example.org",
		Nonce:     "7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v",
		Opaque:    "FQhe/qaU925kfnzjCev0ciny7QMkPqMAFRtzCUYo5tdS",
		Algorithm: "SHA-256",
		QOP:       []string{"auth"},
	}
	header, err := buildSIPDigestAuthorizationWithCnonce(
		challenge, "Mufasa", "Circle of Life", "GET", "/dir/index.html",
		"f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ", 1,
	)
	if err != nil {
		t.Fatalf("buildSIPDigestAuthorizationWithCnonce() error = %v", err)
	}
	if !strings.Contains(header, `response="753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1"`) {
		t.Fatalf("response does not match RFC 7616 vector: %s", header)
	}
	if !strings.Contains(header, "algorithm=SHA-256") {
		t.Fatalf("header missing SHA-256 algorithm: %s", header)
	}
}

func TestBuildSIPDigestAuthorization_Unsupported(t *testing.T) {
	t.Run("unsupported algorithm", func(t *testing.T) {
		challenge := &sipDigestChallenge{Realm: "r", Nonce: "n", Algorithm: "MD5-sess"}
		if _, err := buildSIPDigestAuthorization(challenge, "u", "p", "REGISTER", "sip:r", 1); err == nil {
			t.Fatal("expected error for MD5-sess")
		}
	})

	t.Run("unsupported qop", func(t *testing.T) {
		challenge := &sipDigestChallenge{Realm: "r", Nonce: "n", QOP: []string{"auth-int"}}
		if _, err := buildSIPDigestAuthorization(challenge, "u", "p", "REGISTER", "sip:r", 1); err == nil {
			t.Fatal("expected error for auth-int-only qop")
		}
	})
}

// No-qop (RFC 2069 compatibility) responses omit cnonce/nc.
func TestBuildSIPDigestAuthorization_NoQOP(t *testing.T) {
	challenge := &sipDigestChallenge{Realm: "sip.example.com", Nonce: "n1"}
	header, err := buildSIPDigestAuthorization(challenge, "alice", "secret", "REGISTER", "sip:sip.example.com", 1)
	if err != nil {
		t.Fatalf("buildSIPDigestAuthorization() error = %v", err)
	}
	if strings.Contains(header, "qop=") || strings.Contains(header, "cnonce=") || strings.Contains(header, "nc=") {
		t.Fatalf("no-qop response must omit qop/cnonce/nc: %s", header)
	}
	if !strings.Contains(header, `response="`) {
		t.Fatalf("header missing response: %s", header)
	}
}

// nc is the hex nonce count, eight digits (RFC 2617 §3.2.2), and part of the
// response hash.
func TestBuildSIPDigestAuthorization_NonceCount(t *testing.T) {
	challenge := &sipDigestChallenge{Realm: "r", Nonce: "n", QOP: []string{"auth"}}
	first, err := buildSIPDigestAuthorizationWithCnonce(challenge, "u", "p", "BYE", "sip:x", "c", 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildSIPDigestAuthorizationWithCnonce(challenge, "u", "p", "BYE", "sip:x", "c", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "nc=00000001") || !strings.Contains(second, "nc=00000002") || first == second {
		t.Fatalf("nonce counts not applied:\n%s\n%s", first, second)
	}
	if third, _ := buildSIPDigestAuthorizationWithCnonce(challenge, "u", "p", "BYE", "sip:x", "c", 26); !strings.Contains(third, "nc=0000001a") {
		t.Fatalf("nc is not hex: %s", third)
	}
}

func TestParseSIPDigestChallenge_Stale(t *testing.T) {
	c, err := parseSIPDigestChallenge(`Digest realm="r", nonce="n2", stale=TRUE`)
	if err != nil || !c.Stale {
		t.Fatalf("stale=TRUE: %+v, %v", c, err)
	}
	c, err = parseSIPDigestChallenge(`Digest realm="r", nonce="n2", stale=false`)
	if err != nil || c.Stale {
		t.Fatalf("stale=false: %+v, %v", c, err)
	}
}

// A 407 from a forking proxy can carry one challenge per realm; each gets its
// own Proxy-Authorization. A realm offering SHA-256 and MD5 is answered once,
// with SHA-256, and an unsupported algorithm is skipped.
func TestSIPAuthState_MultiRealmAndAlgorithms(t *testing.T) {
	resp := &sipMessage{StatusCode: 407}
	resp.Add("Proxy-Authenticate", `Digest realm="edge", nonce="a", qop="auth", algorithm=MD5`)
	resp.Add("Proxy-Authenticate", `Digest realm="edge", nonce="b", qop="auth", algorithm=SHA-256`)
	resp.Add("Proxy-Authenticate", `Digest realm="core", nonce="c", algorithm=SHA-512-256`)
	resp.Add("Proxy-Authenticate", `Digest realm="core", nonce="d"`)
	answer, err := newSIPAuthState("u", "p").answer(resp, "INVITE", "sip:100@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(answer.headers) != 2 {
		t.Fatalf("headers = %+v, want one per realm", answer.headers)
	}
	if h := answer.headers[0]; h.Name != "Proxy-Authorization" || !strings.Contains(h.Value, `realm="edge"`) || !strings.Contains(h.Value, "algorithm=SHA-256") || !strings.Contains(h.Value, `nonce="b"`) {
		t.Fatalf("edge credentials = %+v", h)
	}
	if h := answer.headers[1]; !strings.Contains(h.Value, `realm="core"`) || !strings.Contains(h.Value, "algorithm=MD5") {
		t.Fatalf("core credentials = %+v", h)
	}
	if answer.algorithm != "SHA-256" || answer.stale {
		t.Fatalf("answer = %+v", answer)
	}

	only := &sipMessage{StatusCode: 401}
	only.Add("WWW-Authenticate", `Digest realm="r", nonce="n", algorithm=SHA-512-256`)
	if _, err := newSIPAuthState("u", "p").answer(only, "INVITE", "sip:x"); err == nil || !strings.Contains(err.Error(), "unsupported digest algorithm") {
		t.Fatalf("unsupported-only challenge: %v", err)
	}
}

// The same nonce challenged again (the BYE after the INVITE) counts up with
// the same cnonce; a new nonce starts over at 1.
func TestSIPAuthState_NonceCountAcrossRequests(t *testing.T) {
	challenge := func(nonce string) *sipMessage {
		resp := &sipMessage{StatusCode: 407}
		resp.Add("Proxy-Authenticate", `Digest realm="r", nonce="`+nonce+`", qop="auth"`)
		return resp
	}
	auth := newSIPAuthState("u", "p")
	first, _ := auth.answer(challenge("n1"), "INVITE", "sip:x")
	second, _ := auth.answer(challenge("n1"), "BYE", "sip:y")
	third, _ := auth.answer(challenge("n2"), "BYE", "sip:y")
	cnonce := func(a sipAuthAnswer) string {
		_, v, _ := strings.Cut(a.headers[0].Value, "cnonce=")
		v, _, _ = strings.Cut(v, ",")
		return v
	}
	if !strings.Contains(first.headers[0].Value, "nc=00000001") || !strings.Contains(second.headers[0].Value, "nc=00000002") || cnonce(first) != cnonce(second) {
		t.Fatalf("same nonce did not count up:\n%s\n%s", first.headers[0].Value, second.headers[0].Value)
	}
	if !strings.Contains(third.headers[0].Value, "nc=00000001") || cnonce(third) == cnonce(first) {
		t.Fatalf("new nonce did not start over: %s", third.headers[0].Value)
	}
}
