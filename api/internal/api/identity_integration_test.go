//go:build !unit

package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/shared/auth"
)

const identityPassword = "correct-horse-battery" // mcpFixture.createUser's password

type browser struct {
	t    *testing.T
	f    *mcpFixture
	c    *http.Client
	base *url.URL
}

func (f *mcpFixture) browser() *browser {
	jar, _ := cookiejar.New(nil)
	base, _ := url.Parse(f.http.URL)
	return &browser{t: f.t, f: f, c: &http.Client{Jar: jar}, base: base}
}

func (b *browser) do(method, path string, body any) (int, []byte) {
	b.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, b.f.http.URL+path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

func (b *browser) login(username, password string) (int, []byte) {
	b.t.Helper()
	return b.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": username, "password": password})
}

func (b *browser) mustLogin(username string) *browser {
	b.t.Helper()
	if status, body := b.login(username, identityPassword); status != http.StatusOK {
		b.t.Fatalf("login %s: %d %s", username, status, body)
	}
	return b
}

func (b *browser) cookie(name string) string {
	for _, c := range b.c.Jar.Cookies(b.base) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// sid is the session id in the browser's current access token.
func (b *browser) sid() string {
	b.t.Helper()
	claims, err := auth.ParseAdminToken(b.cookie("admin_access"), b.f.server.config.AdminJWTSecret)
	if err != nil {
		b.t.Fatalf("parse access token: %v", err)
	}
	return claims.SessionID
}

func (b *browser) status(method, path string) int {
	b.t.Helper()
	status, _ := b.do(method, path, nil)
	return status
}

func (b *browser) sessions() []models.AdminSession {
	b.t.Helper()
	status, body := b.do(http.MethodGet, "/api/v1/account/sessions", nil)
	if status != http.StatusOK {
		b.t.Fatalf("list sessions: %d %s", status, body)
	}
	var out models.AdminSessionListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		b.t.Fatal(err)
	}
	return out.Items
}

func (f *mcpFixture) userID(username string) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	if err := f.db.QueryRowContext(f.ctx, `SELECT id FROM admin_users WHERE username = $1`, username).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// waitAudit polls the async audit writer for an action on a resource.
func (f *mcpFixture) waitAudit(action, resourceID string) {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var n int
		_ = f.db.QueryRowContext(f.ctx, `
			SELECT COUNT(*) FROM audit_log WHERE action = $1 AND (resource_id = $2 OR actor_id::text = $2)
		`, action, resourceID).Scan(&n)
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("no %s audit event for %s", action, resourceID)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

const authContextPath = "/api/v1/auth-context"

// Unknown user, wrong password and a locked account's correct password all
// get the same answer; the lock shows only to the operator (users list,
// audit), who can lift it.
func TestLoginRefusalsLookAlikeAndTheLockIsAdministered(t *testing.T) {
	f := newMCPFixture(t)
	f.createUser("root", "superadmin", nil)
	f.createUser("alice", "member", nil)
	f.createUser("bob", "member", nil)
	alice := f.userID("alice")
	anon := f.browser()

	for i := 0; i < adminauth.LockoutThreshold; i++ {
		if status, _ := anon.login("alice", "wrong-password-guess"); status != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d", i+1, status)
		}
	}
	unknownStatus, unknownBody := anon.login("nobody-here", identityPassword)
	wrongStatus, wrongBody := anon.login("bob", "wrong-password-guess")
	lockedStatus, lockedBody := anon.login("alice", identityPassword)
	if unknownStatus != http.StatusUnauthorized || wrongStatus != unknownStatus || lockedStatus != unknownStatus {
		t.Fatalf("statuses unknown=%d wrong=%d locked=%d, want all 401", unknownStatus, wrongStatus, lockedStatus)
	}
	if !bytes.Equal(unknownBody, wrongBody) || !bytes.Equal(unknownBody, lockedBody) {
		t.Fatalf("bodies differ:\nunknown %s\nwrong   %s\nlocked  %s", unknownBody, wrongBody, lockedBody)
	}
	f.waitAudit("auth.lockout", alice.String())

	root := f.browser().mustLogin("root")
	status, body := root.do(http.MethodGet, "/api/v1/users/"+alice.String(), nil)
	var user models.AdminUser
	_ = json.Unmarshal(body, &user)
	if status != http.StatusOK || user.LockedUntil == nil || !user.LockedUntil.After(time.Now()) {
		t.Fatalf("user detail: %d locked_until=%v, want a lock in force", status, user.LockedUntil)
	}
	status, body = root.do(http.MethodPost, "/api/v1/users/"+alice.String()+"/unlock", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"was_locked":true`) {
		t.Fatalf("unlock: %d %s", status, body)
	}
	f.waitAudit("user.unlock", alice.String())
	if status, body := anon.login("alice", identityPassword); status != http.StatusOK {
		t.Fatalf("login after unlock: %d %s", status, body)
	}

	// Only superadmins unlock.
	bob := f.browser().mustLogin("bob")
	if status := bob.status(http.MethodPost, "/api/v1/users/"+alice.String()+"/unlock"); status != http.StatusForbidden {
		t.Fatalf("member unlock: %d, want 403", status)
	}
}

// Revoking a session refuses its still-valid access token on the very next
// request, and its refresh token; the user's other sessions and other users
// are untouched.
func TestRevokedSessionLosesAccessAtOnce(t *testing.T) {
	f := newMCPFixture(t)
	f.createUser("alice", "member", nil)
	f.createUser("bob", "member", nil)
	laptop := f.browser().mustLogin("alice")
	phone := f.browser().mustLogin("alice")
	tablet := f.browser().mustLogin("alice")
	bob := f.browser().mustLogin("bob")

	list := laptop.sessions()
	if len(list) != 3 {
		t.Fatalf("alice has %d sessions, want 3", len(list))
	}
	current := 0
	for _, s := range list {
		if s.Current {
			current++
			if s.ID.String() != laptop.sid() {
				t.Fatalf("current session %v is not the laptop's %s", s.ID, laptop.sid())
			}
		}
	}
	if current != 1 {
		t.Fatalf("%d sessions marked current, want 1", current)
	}
	if len(bob.sessions()) != 1 {
		t.Fatal("bob must see only his own session")
	}

	// Another user's session id is not found, not revoked.
	if status := laptop.status(http.MethodDelete, "/api/v1/account/sessions/"+bob.sid()); status != http.StatusNotFound {
		t.Fatalf("revoke bob's session as alice: %d, want 404", status)
	}

	if status := laptop.status(http.MethodDelete, "/api/v1/account/sessions/"+phone.sid()); status != http.StatusNoContent {
		t.Fatalf("revoke phone: %d", status)
	}
	if status := phone.status(http.MethodGet, authContextPath); status != http.StatusUnauthorized {
		t.Fatalf("revoked access token: %d, want 401", status)
	}
	if status := phone.status(http.MethodGet, "/api/v1/auth/me"); status != http.StatusUnauthorized {
		t.Fatalf("revoked /auth/me: %d, want 401", status)
	}
	if status := phone.status(http.MethodPost, "/api/v1/auth/refresh"); status != http.StatusUnauthorized {
		t.Fatalf("revoked refresh: %d, want 401", status)
	}
	if status := laptop.status(http.MethodGet, authContextPath); status != http.StatusOK {
		t.Fatalf("laptop after revoking phone: %d", status)
	}

	status, body := laptop.do(http.MethodPost, "/api/v1/account/sessions/revoke-others", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"revoked":1`) {
		t.Fatalf("revoke others: %d %s", status, body)
	}
	if status := tablet.status(http.MethodGet, authContextPath); status != http.StatusUnauthorized {
		t.Fatalf("tablet after revoke-others: %d, want 401", status)
	}
	if status := laptop.status(http.MethodGet, authContextPath); status != http.StatusOK {
		t.Fatalf("laptop after revoke-others: %d", status)
	}
	if status := bob.status(http.MethodGet, authContextPath); status != http.StatusOK {
		t.Fatalf("bob after alice's revocations: %d", status)
	}
	f.waitAudit("auth.sessions_revoke_others", f.userID("alice").String())

	// Signing out ends the session server-side too.
	laptopAccess := laptop.cookie("admin_access")
	if status := laptop.status(http.MethodPost, "/api/v1/auth/logout"); status != http.StatusNoContent {
		t.Fatalf("logout: %d", status)
	}
	replay := f.browser()
	replay.c.Jar.SetCookies(replay.base, []*http.Cookie{{Name: "admin_access", Value: laptopAccess}})
	if status := replay.status(http.MethodGet, authContextPath); status != http.StatusUnauthorized {
		t.Fatalf("access token replayed after logout: %d, want 401", status)
	}
}

// A refresh continues the same session; a rotated refresh token presented
// again past the grace ends the session.
func TestRefreshKeepsTheSessionAndReuseEndsIt(t *testing.T) {
	f := newMCPFixture(t)
	f.createUser("alice", "member", nil)
	b := f.browser().mustLogin("alice")
	sid := b.sid()
	firstRefresh := b.cookie("admin_refresh")

	if status := b.status(http.MethodPost, "/api/v1/auth/refresh"); status != http.StatusOK {
		t.Fatalf("refresh: %d", status)
	}
	if b.sid() != sid || b.cookie("admin_refresh") == firstRefresh {
		t.Fatalf("after refresh sid=%s (want %s), refresh rotated=%v", b.sid(), sid, b.cookie("admin_refresh") != firstRefresh)
	}
	if n := len(b.sessions()); n != 1 {
		t.Fatalf("%d sessions after a refresh, want 1", n)
	}

	sum := sha256.Sum256([]byte(firstRefresh))
	if _, err := f.db.ExecContext(f.ctx, `
		UPDATE admin_sessions SET revoked_at = NOW() - INTERVAL '5 minutes' WHERE refresh_token_hash = $1
	`, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	thief := f.browser()
	thief.c.Jar.SetCookies(thief.base, []*http.Cookie{{Name: "admin_refresh", Value: firstRefresh}})
	if status := thief.status(http.MethodPost, "/api/v1/auth/refresh"); status != http.StatusUnauthorized {
		t.Fatalf("reused refresh token: %d, want 401", status)
	}
	if status := b.status(http.MethodGet, authContextPath); status != http.StatusUnauthorized {
		t.Fatalf("session after reuse: %d, want 401", status)
	}
	f.waitAudit("auth.session_reuse", sid)
}

// Superadmins list and revoke other users' sessions (REST and MCP); members
// cannot, and API keys never reach /account.
func TestSuperadminManagesAnotherUsersSessions(t *testing.T) {
	f := newMCPFixture(t)
	f.createUser("root", "superadmin", nil)
	f.createUser("alice", "member", []models.MembershipInput{{TenantID: f.tenantA, Role: "viewer"}})
	root := f.browser().mustLogin("root")
	one := f.browser().mustLogin("alice")
	two := f.browser().mustLogin("alice")
	alice := f.userID("alice").String()

	status, body := root.do(http.MethodGet, "/api/v1/users/"+alice+"/sessions", nil)
	var list models.AdminSessionListResponse
	_ = json.Unmarshal(body, &list)
	if status != http.StatusOK || len(list.Items) != 2 || list.Items[0].Current || list.Items[0].AuthMethod != "password" {
		t.Fatalf("superadmin list: %d %s", status, body)
	}
	if status := root.status(http.MethodDelete, "/api/v1/users/"+alice+"/sessions/"+one.sid()); status != http.StatusNoContent {
		t.Fatalf("revoke one: %d", status)
	}
	if status := one.status(http.MethodGet, authContextPath); status != http.StatusUnauthorized {
		t.Fatalf("revoked by superadmin: %d", status)
	}
	if status := one.status(http.MethodGet, "/api/v1/users/"+alice+"/sessions"); status != http.StatusUnauthorized {
		t.Fatalf("revoked member: %d", status)
	}
	if status := two.status(http.MethodGet, "/api/v1/users/"+alice+"/sessions"); status != http.StatusForbidden {
		t.Fatalf("member on /users/{id}/sessions: %d, want 403", status)
	}

	// MCP: the superadmin's agent lists and signs the user out everywhere.
	tokens := f.oauthSignIn("root", identityPassword, "write")
	agent := f.connect(tokens["access_token"].(string))
	if out, isErr := callTool(t, agent, "list_user_sessions", map[string]any{"id": alice}); isErr || !strings.Contains(out, two.sid()) {
		t.Fatalf("list_user_sessions: %v %s", isErr, out)
	}
	if out, isErr := callTool(t, agent, "revoke_user_sessions", map[string]any{"id": alice}); isErr || !strings.Contains(out, `"revoked":1`) {
		t.Fatalf("revoke_user_sessions: %v %s", isErr, out)
	}
	if out, isErr := callTool(t, agent, "unlock_user", map[string]any{"id": alice}); isErr || !strings.Contains(out, `"was_locked":false`) {
		t.Fatalf("unlock_user: %v %s", isErr, out)
	}
	if status := two.status(http.MethodGet, authContextPath); status != http.StatusUnauthorized {
		t.Fatalf("after revoke all: %d", status)
	}
	f.waitAudit("user.sessions_revoke", alice)

	// An API key is not a session.
	key := f.apiKey(f.tenantA, "write")
	req, _ := http.NewRequest(http.MethodGet, f.http.URL+"/api/v1/account/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("API key on /account/sessions: %d, want 403", res.StatusCode)
	}
}

// A superadmin setting someone's password ends all their sessions; setting
// your own keeps the session you did it from.
func TestPasswordResetRevokesSessions(t *testing.T) {
	f := newMCPFixture(t)
	f.createUser("root", "superadmin", nil)
	f.createUser("alice", "member", nil)
	root := f.browser().mustLogin("root")
	rootElsewhere := f.browser().mustLogin("root")
	alice := f.browser().mustLogin("alice")

	newPassword := "another-long-password"
	if status, body := root.do(http.MethodPatch, "/api/v1/users/"+f.userID("alice").String(), map[string]any{"password": newPassword}); status != http.StatusOK {
		t.Fatalf("reset alice: %d %s", status, body)
	}
	if status := alice.status(http.MethodGet, authContextPath); status != http.StatusUnauthorized {
		t.Fatalf("alice after reset: %d, want 401", status)
	}

	if status, body := root.do(http.MethodPatch, "/api/v1/users/"+f.userID("root").String(), map[string]any{"password": newPassword}); status != http.StatusOK {
		t.Fatalf("own password: %d %s", status, body)
	}
	if status := root.status(http.MethodGet, authContextPath); status != http.StatusOK {
		t.Fatalf("own session after own password change: %d, want 200", status)
	}
	if status := rootElsewhere.status(http.MethodGet, authContextPath); status != http.StatusUnauthorized {
		t.Fatalf("other session after own password change: %d, want 401", status)
	}
}

// A refresh token issued before session families (or by an older API binary
// during a rolling deploy) keeps working: the migration or the insert
// trigger gives it a family, and its next refresh carries a sid.
func TestPreFamilyRefreshTokenKeepsWorking(t *testing.T) {
	f := newMCPFixture(t)
	f.createUser("alice", "member", nil)
	alice := f.userID("alice")
	token := "legacy-refresh-token-value"
	sum := sha256.Sum256([]byte(token))
	var rowID uuid.UUID
	if err := f.db.QueryRowContext(f.ctx, `
		INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at)
		VALUES ($1, $2, NOW() + INTERVAL '1 day') RETURNING id
	`, alice, hex.EncodeToString(sum[:])).Scan(&rowID); err != nil {
		t.Fatal(err)
	}
	b := f.browser()
	b.c.Jar.SetCookies(b.base, []*http.Cookie{{Name: "admin_refresh", Value: token}})
	if status, body := b.do(http.MethodPost, "/api/v1/auth/refresh", nil); status != http.StatusOK {
		t.Fatalf("legacy refresh: %d %s", status, body)
	}
	if b.sid() != rowID.String() {
		t.Fatalf("sid = %s, want the legacy row's family %s", b.sid(), rowID)
	}
	if status := b.status(http.MethodGet, authContextPath); status != http.StatusOK {
		t.Fatalf("after legacy refresh: %d", status)
	}

	// An access token minted without a sid (before session families) is
	// still honoured until it expires.
	legacy, err := auth.CreateAdminToken(alice.String(), "", f.server.config.AdminJWTSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	old := f.browser()
	old.c.Jar.SetCookies(old.base, []*http.Cookie{{Name: "admin_access", Value: legacy}})
	if status := old.status(http.MethodGet, authContextPath); status != http.StatusOK {
		t.Fatalf("sid-less access token: %d, want 200", status)
	}
}
