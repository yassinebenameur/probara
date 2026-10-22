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
	// Stale means the credentials were right but the nonce had expired
	// (RFC 2617 §3.2.1): answering again with the new nonce is expected,
	// not a sign of a wrong password.
	Stale bool
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
		Stale:     strings.EqualFold(strings.TrimSpace(params["stale"]), "true"),
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
// with qop=auth or no qop (RFC 2069 compatibility). nc is the nonce count:
// 1 for the first request answering a nonce, one more for each later request
// reusing it (sipAuthState keeps the count).
func buildSIPDigestAuthorization(challenge *sipDigestChallenge, username, password, method, uri string, nc int) (string, error) {
	cnonce, err := newSIPCnonce()
	if err != nil {
		return "", err
	}
	return buildSIPDigestAuthorizationWithCnonce(challenge, username, password, method, uri, cnonce, nc)
}

// buildSIPDigestAuthorizationWithCnonce is the deterministic core of
// buildSIPDigestAuthorization, split out so tests can verify against
// published RFC vectors.
func buildSIPDigestAuthorizationWithCnonce(challenge *sipDigestChallenge, username, password, method, uri, cnonce string, nc int) (string, error) {
	if nc < 1 {
		nc = 1
	}
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
		ncValue := fmt.Sprintf("%08x", nc)
		response = digest(strings.Join([]string{ha1, challenge.Nonce, ncValue, cnonce, "auth", ha2}, ":"))
		params = append(params,
			"qop=auth",
			"nc="+ncValue,
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

// sipSupportedDigestAlgorithm ranks the algorithms this client answers:
// SHA-256 over MD5 when a realm offers both (RFC 8760 §2.4), 0 for one it
// cannot answer.
func sipSupportedDigestAlgorithm(algorithm string) int {
	switch strings.ToUpper(strings.TrimSpace(algorithm)) {
	case "SHA-256":
		return 2
	case "", "MD5":
		return 1
	default:
		return 0
	}
}

// sipAuthState answers the digest challenges of one exchange with the
// server — a check's REGISTER or OPTIONS, or a call and everything in its
// dialogs. It remembers each realm's challenge, client nonce and nonce
// count, so a later request challenged with a nonce already answered (a BYE
// after the INVITE) counts up (nc=00000002) instead of replaying nc=1, which
// a server enforcing RFC 2617 §3.2.2 would take for a replay.
type sipAuthState struct {
	username string
	password string
	realms   map[string]*sipAuthRealm // key: sipAuthKey(credentials header, realm)
	order    []string                 // realms keys, first-seen
}

func sipAuthKey(credentialsHeader, realm string) string {
	return credentialsHeader + "\x00" + realm
}

type sipAuthRealm struct {
	header    string // Authorization or Proxy-Authorization
	challenge *sipDigestChallenge
	cnonce    string
	nc        int
}

func newSIPAuthState(username, password string) *sipAuthState {
	return &sipAuthState{username: username, password: password, realms: make(map[string]*sipAuthRealm)}
}

// sipAuthAnswer is the credentials answering one 401/407.
type sipAuthAnswer struct {
	headers   []sipHeader
	keys      []string // the realm keys headers answer, in the same order
	algorithm string   // the strongest algorithm used, as the envelope names it
	// stale: every challenge in the response was marked stale=true.
	stale bool
}

// answer builds credentials for every realm a 401 (WWW-Authenticate) or 407
// (Proxy-Authenticate) challenges, one header per realm (RFC 3261 §22.3: a
// forking proxy collects one challenge per realm), for a request with method
// and Request-URI uri. A realm offering several algorithms is answered once,
// with the strongest supported. It fails when the response carries no
// challenge or none this client can answer.
func (a *sipAuthState) answer(resp *sipMessage, method, uri string) (sipAuthAnswer, error) {
	challengeHeader, credentialsHeader := "WWW-Authenticate", "Authorization"
	if resp.StatusCode == 407 {
		challengeHeader, credentialsHeader = "Proxy-Authenticate", "Proxy-Authorization"
	}
	values := resp.Values(challengeHeader)
	if len(values) == 0 {
		return sipAuthAnswer{}, fmt.Errorf("server returned %d without an authentication challenge", resp.StatusCode)
	}

	// The best supported challenge per realm, in first-seen order.
	var (
		order    []string
		best     = make(map[string]*sipDigestChallenge)
		firstErr error
		allStale = true
	)
	for _, value := range values {
		challenge, err := parseSIPDigestChallenge(value)
		if err == nil && sipSupportedDigestAlgorithm(challenge.Algorithm) == 0 {
			err = fmt.Errorf("unsupported digest algorithm %q", challenge.Algorithm)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		current, seen := best[challenge.Realm]
		if !seen {
			order = append(order, challenge.Realm)
		}
		if !seen || sipSupportedDigestAlgorithm(challenge.Algorithm) > sipSupportedDigestAlgorithm(current.Algorithm) {
			best[challenge.Realm] = challenge
		}
	}
	if len(order) == 0 {
		return sipAuthAnswer{}, firstErr
	}

	var out sipAuthAnswer
	for _, realm := range order {
		challenge := best[realm]
		allStale = allStale && challenge.Stale
		key := sipAuthKey(credentialsHeader, realm)
		state := a.realms[key]
		if state == nil || state.challenge.Nonce != challenge.Nonce {
			cnonce, err := newSIPCnonce()
			if err != nil {
				return sipAuthAnswer{}, err
			}
			if state == nil {
				a.order = append(a.order, key)
			}
			state = &sipAuthRealm{header: credentialsHeader, challenge: challenge, cnonce: cnonce}
			a.realms[key] = state
		}
		state.nc++
		value, err := buildSIPDigestAuthorizationWithCnonce(challenge, a.username, a.password, method, uri, state.cnonce, state.nc)
		if err != nil {
			return sipAuthAnswer{}, err
		}
		out.headers = append(out.headers, sipHeader{Name: credentialsHeader, Value: value})
		out.keys = append(out.keys, key)
		if name := sipDigestAlgorithmName(challenge.Algorithm); out.algorithm == "" || name == "SHA-256" {
			out.algorithm = name
		}
	}
	out.stale = allStale
	return out, nil
}

// credentials is every credential a new request in the exchange carries:
// ans's headers for the realms it just answered, and a fresh answer (the
// realm's next nonce count, over this request's method and uri) for every
// other realm answered earlier, all in first-seen order. A retry challenged
// by one hop (a stale nonce, a BYE challenged for one realm) so keeps the
// credentials every other hop already accepted (RFC 3261 §22.3).
func (a *sipAuthState) credentials(ans sipAuthAnswer, method, uri string) ([]sipHeader, error) {
	fresh := make(map[string]sipHeader, len(ans.keys))
	for i, key := range ans.keys {
		fresh[key] = ans.headers[i]
	}
	out := make([]sipHeader, 0, len(a.order))
	for _, key := range a.order {
		if h, ok := fresh[key]; ok {
			out = append(out, h)
			continue
		}
		state := a.realms[key]
		state.nc++
		value, err := buildSIPDigestAuthorizationWithCnonce(state.challenge, a.username, a.password, method, uri, state.cnonce, state.nc)
		if err != nil {
			return nil, err
		}
		out = append(out, sipHeader{Name: state.header, Value: value})
	}
	return out, nil
}

// unansweredRealm is the first realm a 401/407 challenges that this exchange
// never answered (a further hop asking for its own credentials), or false
// when every challenged realm was answered before.
func (a *sipAuthState) unansweredRealm(resp *sipMessage) (string, bool) {
	credentialsHeader := "Authorization"
	if resp.StatusCode == 407 {
		credentialsHeader = "Proxy-Authorization"
	}
	for _, value := range resp.Values(sipChallengeHeader(resp.StatusCode)) {
		challenge, err := parseSIPDigestChallenge(value)
		if err != nil {
			continue
		}
		if _, known := a.realms[sipAuthKey(credentialsHeader, challenge.Realm)]; !known {
			return challenge.Realm, true
		}
	}
	return "", false
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
