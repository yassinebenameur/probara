// Package webpush implements the Web Push protocol: RFC 8291 message
// encryption (aes128gcm) and RFC 8292 VAPID application server
// authentication.
//
// This is implemented here rather than pulled in as a dependency because
// every primitive it needs is already vendored -- crypto/ecdh and crypto/aes
// from the standard library, HKDF from golang.org/x/crypto, and ES256 signing
// from golang-jwt/jwt/v5, all of which the repo already uses.
//
// The risk in Web Push is not the primitives, it is the framing: the exact
// HKDF info strings, the aes128gcm header layout, and the padding delimiter.
// Every one of those fails *silently* -- the push service returns 201 and the
// notification simply never arrives. The package is therefore validated
// against the published test vectors in RFC 8291 section 5 and RFC 8188
// section 3.1 (see webpush_test.go), which is the only way to be sure the
// framing is right without a live push service.
package webpush

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// vapidTokenTTL is how long a signed VAPID JWT stays valid. RFC 8292 section
// 2 caps "exp" at 24 hours from issue; push services reject anything beyond
// that. Twelve hours leaves generous room for clock skew in either direction
// while still letting one token cover a long batch of sends.
const vapidTokenTTL = 12 * time.Hour

// Keys is a VAPID application server keypair.
//
// Public is the uncompressed P-256 point (65 bytes, 0x04-prefixed) that the
// browser receives as applicationServerKey. It is not a credential: it ships
// inside every rendered status page.
//
// Private is the raw 32-byte scalar. Both are base64url, unpadded, which is
// the encoding browsers and push services expect.
type Keys struct {
	Public  string
	Private string
}

// GenerateKeys mints a new VAPID keypair.
//
// Callers must persist the result and supply it as configuration. Generating
// at process start is always a bug: with more than one replica each would
// mint a different pair, so a subscription created against one replica is
// unusable by another, and a restart silently invalidates every stored
// subscription (push services bind an endpoint to the key that created it).
func GenerateKeys() (Keys, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Keys{}, fmt.Errorf("generate p256 key: %w", err)
	}

	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // uncompressed point is the wire format Web Push requires
	// Left-pad to the full 32 bytes: a scalar with leading zero bytes would
	// otherwise round-trip to a shorter key that push services reject.
	d := make([]byte, 32)
	priv.D.FillBytes(d)

	return Keys{
		Public:  base64.RawURLEncoding.EncodeToString(pub),
		Private: base64.RawURLEncoding.EncodeToString(d),
	}, nil
}

// parsePrivateKey decodes a base64url raw 32-byte scalar into an ECDSA key,
// deriving the public point from it.
func parsePrivateKey(encoded string) (*ecdsa.PrivateKey, error) {
	raw, err := decodeBase64(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode vapid private key: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("vapid private key must be 32 bytes, got %d", len(raw))
	}

	d := new(big.Int).SetBytes(raw)
	curve := elliptic.P256()
	if d.Sign() == 0 || d.Cmp(curve.Params().N) >= 0 {
		return nil, fmt.Errorf("vapid private key is out of range for P-256")
	}

	priv := &ecdsa.PrivateKey{D: d}
	priv.PublicKey.Curve = curve
	priv.PublicKey.X, priv.PublicKey.Y = curve.ScalarBaseMult(raw)
	return priv, nil
}

// AuthorizationHeader builds the RFC 8292 "vapid" Authorization header value
// for one push endpoint.
//
// The "aud" claim is the endpoint's origin (scheme + host, no path): push
// services reject a token whose audience is the full endpoint URL. "sub" is
// the operator contact, which some services reject when missing or malformed,
// so it is validated here rather than at the far end.
func AuthorizationHeader(endpoint, subject string, keys Keys) (string, error) {
	aud, err := audienceFor(endpoint)
	if err != nil {
		return "", err
	}
	sub, err := normalizeSubject(subject)
	if err != nil {
		return "", err
	}
	priv, err := parsePrivateKey(keys.Private)
	if err != nil {
		return "", err
	}

	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"aud": aud,
		"exp": time.Now().Add(vapidTokenTTL).Unix(),
		"sub": sub,
	})
	signed, err := token.SignedString(priv)
	if err != nil {
		return "", fmt.Errorf("sign vapid token: %w", err)
	}

	// RFC 8292 section 3.1: a single "vapid" credential carrying both the
	// token and the public key. The older two-header form (Authorization:
	// WebPush + Crypto-Key) is obsolete and rejected by newer services.
	return fmt.Sprintf("vapid t=%s, k=%s", signed, keys.Public), nil
}

// audienceFor reduces a push endpoint to the scheme://host origin RFC 8292
// requires as the JWT audience.
func audienceFor(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse push endpoint: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("push endpoint must be an https URL with a host")
	}
	return u.Scheme + "://" + u.Host, nil
}

// normalizeSubject validates the VAPID "sub" contact. RFC 8292 section 2.1
// allows a mailto: or https: URI; anything else is rejected outright rather
// than passed through to fail opaquely at the push service.
func normalizeSubject(subject string) (string, error) {
	sub := strings.TrimSpace(subject)
	if sub == "" {
		return "", fmt.Errorf("vapid subject is required (mailto: or https: contact)")
	}
	if !strings.HasPrefix(sub, "mailto:") && !strings.HasPrefix(sub, "https://") {
		return "", fmt.Errorf("vapid subject must be a mailto: or https: URI, got %q", sub)
	}
	return sub, nil
}

// decodeBase64 accepts both padded and unpadded base64url. Browsers emit
// unpadded keys, but hand-configured VAPID keys are routinely pasted with
// padding, and rejecting those would be a confusing failure at boot.
func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, "+/") {
		// Tolerate standard-alphabet input for the same reason.
		if raw, err := base64.StdEncoding.DecodeString(s); err == nil {
			return raw, nil
		}
		return base64.RawStdEncoding.DecodeString(s)
	}
	if raw, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return raw, nil
	}
	return base64.URLEncoding.DecodeString(s)
}
