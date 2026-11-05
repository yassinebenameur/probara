package adminauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 default; every authenticator app speaks HMAC-SHA1
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// RFC 6238 TOTP with the parameters every authenticator app accepts:
// HMAC-SHA1, 6 digits, 30-second steps. Implemented on the standard library
// (it is a few lines of RFC 4226), so the second factor adds no dependency.
const (
	totpPeriod = 30
	totpDigits = 6
	// totpSkew accepts the previous and the next step as well, for clock
	// drift between the server and the phone.
	totpSkew = 1
	// totpSecretBytes is the RFC 4226 recommended 160-bit key.
	totpSecretBytes = 20
	// TOTPIssuer names the account in the authenticator app.
	TOTPIssuer = "Probara"
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret returns a fresh base32 (no padding) TOTP key.
func newTOTPSecret() (string, error) {
	key := make([]byte, totpSecretBytes)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generate TOTP secret: %w", err)
	}
	return totpEncoding.EncodeToString(key), nil
}

// hotp is RFC 4226: HMAC-SHA1 over the big-endian counter, dynamic
// truncation, modulo 10^digits, zero-padded.
func hotp(key []byte, counter uint64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, bin%mod)
}

// totpStep is the RFC 6238 time step containing t.
func totpStep(t time.Time) int64 {
	return t.Unix() / totpPeriod
}

// decodeTOTPSecret accepts the base32 secret with or without padding,
// spaces and case, the way people type it from an app's "manual entry".
func decodeTOTPSecret(secret string) ([]byte, error) {
	clean := strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(secret))
	clean = strings.TrimRight(clean, "=")
	key, err := totpEncoding.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("invalid TOTP secret: %w", err)
	}
	return key, nil
}

// normalizeTOTPCode strips the spaces some apps show inside a code. It
// returns "" for anything that is not exactly totpDigits digits.
func normalizeTOTPCode(code string) string {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return ""
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return code
}

// matchTOTP checks code against the steps around now and returns the step
// it matched. Only steps after lastStep count (lastStep < 0: none used
// yet), which is the replay protection: a code accepted once, or any code
// older than it, is refused. Every candidate is compared in constant time.
func matchTOTP(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	code = normalizeTOTPCode(code)
	if code == "" {
		return 0, false
	}
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return 0, false
	}
	current := totpStep(now)
	matched, found := int64(0), false
	for step := current - totpSkew; step <= current+totpSkew; step++ {
		if step < 0 {
			continue
		}
		want := hotp(key, uint64(step), totpDigits)
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 && step > lastStep && !found {
			matched, found = step, true
		}
	}
	return matched, found
}

// TOTPURI is the otpauth:// URI an authenticator app enrolls from (the
// QR code the operator UI renders client-side).
func TOTPURI(account, secret string) string {
	label := url.PathEscape(TOTPIssuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", TOTPIssuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TOTPCodeAt is the code an authenticator app shows for secret at t (tests
// and tooling; the server only ever verifies).
func TOTPCodeAt(secret string, t time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	return hotp(key, uint64(totpStep(t)), totpDigits), nil
}
