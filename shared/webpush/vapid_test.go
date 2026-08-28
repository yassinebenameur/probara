package webpush

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestGenerateKeys_ProducesUsableP256Material(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() error = %v", err)
	}

	pub, err := base64.RawURLEncoding.DecodeString(keys.Public)
	if err != nil {
		t.Fatalf("public key is not unpadded base64url: %v", err)
	}
	// Browsers pass this straight to pushManager.subscribe as
	// applicationServerKey, which requires the uncompressed 65-byte form.
	if len(pub) != 65 || pub[0] != 0x04 {
		t.Fatalf("public key is %d bytes starting %#x, want 65 starting 0x04", len(pub), pub[0])
	}

	priv, err := base64.RawURLEncoding.DecodeString(keys.Private)
	if err != nil {
		t.Fatalf("private key is not unpadded base64url: %v", err)
	}
	if len(priv) != 32 {
		t.Fatalf("private key is %d bytes, want 32", len(priv))
	}
}

func TestGenerateKeys_KeysAreDistinct(t *testing.T) {
	// Guards the reason keys must be configured rather than generated at
	// boot: two replicas generating independently do not agree, so a
	// subscription made against one is unusable by the other.
	a, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() error = %v", err)
	}
	b, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() error = %v", err)
	}
	if a.Public == b.Public || a.Private == b.Private {
		t.Fatalf("two generated keypairs are identical")
	}
}

func TestAuthorizationHeader_SignsVerifiableES256Token(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() error = %v", err)
	}

	header, err := AuthorizationHeader("https://fcm.googleapis.com/fcm/send/abc123", "mailto:ops@example.com", keys)
	if err != nil {
		t.Fatalf("AuthorizationHeader() error = %v", err)
	}

	// RFC 8292 section 3.1: one "vapid" credential carrying t= and k=. The
	// obsolete two-header form is rejected by newer push services.
	if !strings.HasPrefix(header, "vapid t=") || !strings.Contains(header, ", k=") {
		t.Fatalf("header = %q, want the single-credential vapid form", header)
	}
	tokenStr := strings.TrimPrefix(strings.Split(header, ", k=")[0], "vapid t=")
	if got := strings.Split(header, ", k=")[1]; got != keys.Public {
		t.Fatalf("k= is %q, want the public key %q", got, keys.Public)
	}

	claims := jwt.MapClaims{}
	_, err = jwt.ParseWithClaims(tokenStr, claims, func(tok *jwt.Token) (any, error) {
		if tok.Method.Alg() != "ES256" {
			t.Fatalf("alg = %q, want ES256", tok.Method.Alg())
		}
		return publicKeyFrom(t, keys.Public), nil
	})
	if err != nil {
		t.Fatalf("token does not verify against its own public key: %v", err)
	}

	// The audience must be the endpoint ORIGIN, not the full URL. Push
	// services reject a token whose aud carries the path.
	if got := claims["aud"]; got != "https://fcm.googleapis.com" {
		t.Fatalf("aud = %v, want the endpoint origin without a path", got)
	}
	if got := claims["sub"]; got != "mailto:ops@example.com" {
		t.Fatalf("sub = %v, want the configured contact", got)
	}

	exp, ok := claims["exp"].(float64)
	if !ok {
		t.Fatalf("exp claim is missing or not numeric: %v", claims["exp"])
	}
	// RFC 8292 caps exp at 24h from issue; services reject anything beyond.
	if d := time.Until(time.Unix(int64(exp), 0)); d <= 0 || d > 24*time.Hour {
		t.Fatalf("exp is %v from now, want positive and within 24h", d)
	}
}

func TestAuthorizationHeader_RejectsBadInput(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() error = %v", err)
	}

	tests := []struct {
		name     string
		endpoint string
		subject  string
		keys     Keys
	}{
		// A plaintext endpoint would leak the payload and is never valid.
		{"http endpoint", "http://push.example.com/x", "mailto:o@example.com", keys},
		{"not a url", "://nope", "mailto:o@example.com", keys},
		{"no host", "https:///x", "mailto:o@example.com", keys},
		// A missing or malformed sub is rejected by some push services with
		// an opaque error, so catch it here instead.
		{"empty subject", "https://push.example.com/x", "", keys},
		{"subject not a uri", "https://push.example.com/x", "ops@example.com", keys},
		{"subject wrong scheme", "https://push.example.com/x", "tel:+15551234", keys},
		{"private key not base64", "https://push.example.com/x", "mailto:o@example.com", Keys{Public: keys.Public, Private: "!!!"}},
		{"private key wrong length", "https://push.example.com/x", "mailto:o@example.com", Keys{Public: keys.Public, Private: base64.RawURLEncoding.EncodeToString([]byte("short"))}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := AuthorizationHeader(tt.endpoint, tt.subject, tt.keys); err == nil {
				t.Fatalf("AuthorizationHeader() succeeded; want error")
			}
		})
	}
}

func TestAuthorizationHeader_AcceptsHTTPSSubject(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() error = %v", err)
	}
	if _, err := AuthorizationHeader("https://push.example.com/x", "https://example.com/contact", keys); err != nil {
		t.Fatalf("AuthorizationHeader() rejected a valid https: subject: %v", err)
	}
}

func TestDecodeBase64_AcceptsPaddedAndUnpadded(t *testing.T) {
	// Browsers emit unpadded base64url, but operators routinely paste keys
	// with padding. Rejecting those would fail confusingly at boot.
	want := "hello world!!"
	for name, encoded := range map[string]string{
		"raw url":     base64.RawURLEncoding.EncodeToString([]byte(want)),
		"padded url":  base64.URLEncoding.EncodeToString([]byte(want)),
		"padded std":  base64.StdEncoding.EncodeToString([]byte(want)),
		"raw std":     base64.RawStdEncoding.EncodeToString([]byte(want)),
		"with spaces": "  " + base64.RawURLEncoding.EncodeToString([]byte(want)) + "  ",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := decodeBase64(encoded)
			if err != nil {
				t.Fatalf("decodeBase64(%q) error = %v", encoded, err)
			}
			if string(got) != want {
				t.Fatalf("decodeBase64(%q) = %q, want %q", encoded, got, want)
			}
		})
	}
}

func publicKeyFrom(t *testing.T, encoded string) *ecdsa.PublicKey {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode public key: %v", err)
	}
	return &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(raw[1:33]),
		Y:     new(big.Int).SetBytes(raw[33:]),
	}
}
