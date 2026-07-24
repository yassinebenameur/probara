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
		challenge, "Mufasa", "Circle Of Life", "GET", "/dir/index.html", "0a4f113b",
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
		"f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ",
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
		if _, err := buildSIPDigestAuthorization(challenge, "u", "p", "REGISTER", "sip:r"); err == nil {
			t.Fatal("expected error for MD5-sess")
		}
	})

	t.Run("unsupported qop", func(t *testing.T) {
		challenge := &sipDigestChallenge{Realm: "r", Nonce: "n", QOP: []string{"auth-int"}}
		if _, err := buildSIPDigestAuthorization(challenge, "u", "p", "REGISTER", "sip:r"); err == nil {
			t.Fatal("expected error for auth-int-only qop")
		}
	})
}

// No-qop (RFC 2069 compatibility) responses omit cnonce/nc.
func TestBuildSIPDigestAuthorization_NoQOP(t *testing.T) {
	challenge := &sipDigestChallenge{Realm: "sip.example.com", Nonce: "n1"}
	header, err := buildSIPDigestAuthorization(challenge, "alice", "secret", "REGISTER", "sip:sip.example.com")
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
