package worker

import (
	"crypto/md5" //nolint:gosec // MD5 is mandated by RFC 3261 digest auth
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// sipDigestChallenge holds the parameters of a Digest authentication
// challenge from a WWW-Authenticate or Proxy-Authenticate header
// (RFC 3261 §22, RFC 7616/8760 for SHA-256).
type sipDigestChallenge struct {
	Realm     string
	Nonce     string
	Opaque    string
	Algorithm string   // "" (implies MD5), "MD5", or "SHA-256"
	QOP       []string // offered qop values, e.g. ["auth", "auth-int"]
}

// parseSIPDigestChallenge parses a `Digest realm="…", nonce="…"` header value.
func parseSIPDigestChallenge(header string) (*sipDigestChallenge, error) {
	trimmed := strings.TrimSpace(header)
	if len(trimmed) < 7 || !strings.EqualFold(trimmed[:6], "digest") || !isSIPWhitespace(trimmed[6]) {
		scheme := trimmed
		if idx := strings.IndexAny(scheme, " \t"); idx > 0 {
			scheme = scheme[:idx]
		}
		return nil, fmt.Errorf("unsupported authentication scheme %q (only Digest is supported)", scheme)
	}

	params := parseSIPAuthParams(trimmed[7:])
	challenge := &sipDigestChallenge{
		Realm:     params["realm"],
		Nonce:     params["nonce"],
		Opaque:    params["opaque"],
		Algorithm: params["algorithm"],
	}
	if qop := params["qop"]; qop != "" {
		for _, value := range strings.Split(qop, ",") {
			value = strings.TrimSpace(value)
			if value != "" {
				challenge.QOP = append(challenge.QOP, value)
			}
		}
	}
	if challenge.Nonce == "" {
		return nil, fmt.Errorf("digest challenge is missing a nonce")
	}
	return challenge, nil
}

// buildSIPDigestAuthorization computes the credentials header value answering
// a digest challenge. Supports MD5 (RFC 3261 default) and SHA-256 (RFC 8760),
// with qop=auth or no qop (RFC 2069 compatibility).
func buildSIPDigestAuthorization(challenge *sipDigestChallenge, username, password, method, uri string) (string, error) {
	cnonce, err := newSIPCnonce()
	if err != nil {
		return "", err
	}
	return buildSIPDigestAuthorizationWithCnonce(challenge, username, password, method, uri, cnonce)
}

// buildSIPDigestAuthorizationWithCnonce is the deterministic core of
// buildSIPDigestAuthorization, split out so tests can verify against
// published RFC vectors.
func buildSIPDigestAuthorizationWithCnonce(challenge *sipDigestChallenge, username, password, method, uri, cnonce string) (string, error) {
	algorithm := strings.ToUpper(strings.TrimSpace(challenge.Algorithm))
	var digest func(string) string
	switch algorithm {
	case "", "MD5":
		algorithm = "MD5"
		digest = func(s string) string {
			sum := md5.Sum([]byte(s)) //nolint:gosec // RFC 3261 digest auth
			return hex.EncodeToString(sum[:])
		}
	case "SHA-256":
		digest = func(s string) string {
			sum := sha256.Sum256([]byte(s))
			return hex.EncodeToString(sum[:])
		}
	default:
		return "", fmt.Errorf("unsupported digest algorithm %q", challenge.Algorithm)
	}

	ha1 := digest(username + ":" + challenge.Realm + ":" + password)
	ha2 := digest(method + ":" + uri)

	params := []string{
		fmt.Sprintf("username=%q", username),
		fmt.Sprintf("realm=%q", challenge.Realm),
		fmt.Sprintf("nonce=%q", challenge.Nonce),
		fmt.Sprintf("uri=%q", uri),
	}

	var response string
	if len(challenge.QOP) > 0 {
		if !sipQOPOffersAuth(challenge.QOP) {
			return "", fmt.Errorf("no supported qop offered (got %s; only auth is supported)", strings.Join(challenge.QOP, ","))
		}
		const nc = "00000001"
		response = digest(strings.Join([]string{ha1, challenge.Nonce, nc, cnonce, "auth", ha2}, ":"))
		params = append(params,
			"qop=auth",
			"nc="+nc,
			fmt.Sprintf("cnonce=%q", cnonce),
		)
	} else {
		response = digest(ha1 + ":" + challenge.Nonce + ":" + ha2)
	}

	params = append(params, fmt.Sprintf("response=%q", response), "algorithm="+algorithm)
	if challenge.Opaque != "" {
		params = append(params, fmt.Sprintf("opaque=%q", challenge.Opaque))
	}
	return "Digest " + strings.Join(params, ", "), nil
}

// parseSIPAuthParams splits `key="value", key2=value2` pairs, honoring commas
// inside quoted strings.
func parseSIPAuthParams(input string) map[string]string {
	params := make(map[string]string)
	rest := input
	for rest != "" {
		rest = strings.TrimLeft(rest, " \t,")
		if rest == "" {
			break
		}
		eq := strings.IndexByte(rest, '=')
		if eq < 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(rest[:eq]))
		rest = strings.TrimLeft(rest[eq+1:], " \t")

		var value string
		if strings.HasPrefix(rest, `"`) {
			end := 1
			var b strings.Builder
			for end < len(rest) {
				ch := rest[end]
				if ch == '\\' && end+1 < len(rest) {
					b.WriteByte(rest[end+1])
					end += 2
					continue
				}
				if ch == '"' {
					break
				}
				b.WriteByte(ch)
				end++
			}
			value = b.String()
			if end < len(rest) {
				end++ // consume closing quote
			}
			rest = rest[end:]
		} else {
			end := strings.IndexByte(rest, ',')
			if end < 0 {
				value = strings.TrimSpace(rest)
				rest = ""
			} else {
				value = strings.TrimSpace(rest[:end])
				rest = rest[end:]
			}
		}
		if key != "" {
			params[key] = value
		}
	}
	return params
}

func sipQOPOffersAuth(qop []string) bool {
	for _, value := range qop {
		if strings.EqualFold(value, "auth") {
			return true
		}
	}
	return false
}

func newSIPCnonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate cnonce: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func isSIPWhitespace(b byte) bool {
	return b == ' ' || b == '\t'
}
