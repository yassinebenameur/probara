//go:build !unit

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/config"
)

// A refresh that carries an access cookie this server signed skips the rate
// limiter's address pre-check, but only the pre-check: the refresh handler
// still checks the session family, so a family ended by a revocation (sign-out
// elsewhere, password reset, MCP-wide sign-out) cannot refresh, even from an
// address whose failure budget is spent -- and it gets a 401, not a 429, so
// the browser goes to sign in. A live session from the same address refreshes.
func TestVouchedRefreshCannotReviveARevokedSession(t *testing.T) {
	f := newMCPFixtureWith(t, func(cfg *config.APIConfig) {
		cfg.RateLimitPerMinute = 60
		cfg.RateLimitBurst = 4
		cfg.TrustedProxyCIDRs = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	})
	const address = "198.51.100.77" // a public client behind the trusted proxy
	send := func(c *http.Client, method, path, bearer string, body any) int {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, err := http.NewRequest(method, f.http.URL+path, bytes.NewReader(raw))
		require.NoError(t, err)
		req.Header.Set("X-Forwarded-For", address)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		res, err := c.Do(req)
		require.NoError(t, err)
		res.Body.Close()
		return res.StatusCode
	}
	signIn := func(username string) *http.Client {
		f.createUser(username, auth.PlatformRoleMember, []models.MembershipInput{{TenantID: f.tenantA, Role: auth.RoleViewer}})
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar}
		require.Equal(t, http.StatusOK, send(c, http.MethodPost, "/api/v1/auth/login", "",
			map[string]string{"username": username, "password": "correct-horse-battery"}))
		return c
	}
	revoked, live := signIn("revoked-user"), signIn("live-user")
	_, err := f.db.ExecContext(f.ctx, `
		UPDATE admin_session_families SET revoked_at = NOW()
		WHERE admin_user_id = (SELECT id FROM admin_users WHERE username = 'revoked-user')`)
	require.NoError(t, err)

	// Spend the address's failure budget with bogus keys.
	for i := 0; i < 4; i++ {
		send(http.DefaultClient, http.MethodGet, "/api/v1/monitors", "pk_bogus", nil)
	}
	require.Equal(t, http.StatusTooManyRequests, send(http.DefaultClient, http.MethodGet, "/api/v1/monitors", "pk_bogus", nil))

	require.Equal(t, http.StatusUnauthorized, send(revoked, http.MethodPost, "/api/v1/auth/refresh", "", nil),
		"a revoked session family is refused by the handler, and the answer stays 401")
	require.Equal(t, http.StatusOK, send(live, http.MethodPost, "/api/v1/auth/refresh", "", nil),
		"a live session refreshes from the flooded address")
}
