//go:build !unit

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/shared/auth"
)

const identityPassword = "correct-horse-battery"

// browser is a cookie-session client of the fixture's API.
type browser struct {
	f      *mcpFixture
	client *http.Client
}

func (f *mcpFixture) signIn(username string) *browser {
	f.t.Helper()
	jar, _ := cookiejar.New(nil)
	b := &browser{f: f, client: &http.Client{Jar: jar}}
	postJSON(f.t, b.client, f.http.URL+"/api/v1/auth/login", nil,
		map[string]any{"username": username, "password": identityPassword}, http.StatusOK, nil)
	return b
}

func (b *browser) do(method, path string, body any) (int, []byte) {
	b.f.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, b.f.http.URL+path, reader)
	req.Header.Set("Content-Type", "application/json")
	res, err := b.client.Do(req)
	if err != nil {
		b.f.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

func (b *browser) expect(method, path string, want int, out any) {
	b.f.t.Helper()
	status, data := b.do(method, path, nil)
	if status != want {
		b.f.t.Fatalf("%s %s: want %d, got %d: %s", method, path, want, status, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			b.f.t.Fatalf("%s %s: %v: %s", method, path, err, data)
		}
	}
}

// accessSID decodes the sid claim of the browser's current access cookie.
func (b *browser) accessSID() string {
	b.f.t.Helper()
	u, _ := url.Parse(b.f.http.URL)
	for _, c := range b.client.Jar.Cookies(u) {
		if c.Name == "admin_access" {
			claims, err := auth.ParseAdminToken(c.Value, b.f.server.config.AdminJWTSecret)
			if err != nil {
				b.f.t.Fatal(err)
			}
			return claims.SessionID
		}
	}
	b.f.t.Fatal("no access cookie")
	return ""
}

func (b *browser) currentSession() models.AdminSession {
	b.f.t.Helper()
	var list models.AdminSessionListResponse
	b.expect(http.MethodGet, "/api/v1/account/sessions", http.StatusOK, &list)
	for _, s := range list.Items {
		if s.Current {
			return s
		}
	}
	b.f.t.Fatalf("no current session in %+v", list.Items)
	return models.AdminSession{}
}

func (f *mcpFixture) userID(username string) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	if err := f.db.QueryRowContext(f.ctx, `SELECT id FROM admin_users WHERE username = $1`, username).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// waitForAudit polls the async audit writer for an action on a user.
func (f *mcpFixture) waitForAudit(action string, userID uuid.UUID) {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		_ = f.db.QueryRowContext(f.ctx, `
			SELECT COUNT(*) FROM audit_log WHERE action = $1 AND resource_id = $2
		`, action, userID.String()).Scan(&n)
		if n > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	f.t.Fatalf("no %s audit event for %s", action, userID)
}

func TestSessionRevocationEndsAccessTokensImmediately(t *testing.T) {
	f := newMCPFixture(t)
	f.createUser("root", "superadmin", nil)
	f.createUser("alice", "member", []models.MembershipInput{{TenantID: f.tenantA, Role: "viewer"}})

	laptop := f.signIn("alice")
	phone := f.signIn("alice")

	var list models.AdminSessionListResponse
	laptop.expect(http.MethodGet, "/api/v1/account/sessions", http.StatusOK, &list)
	if len(list.Items) != 2 {
		t.Fatalf("sessions = %+v, want 2", list.Items)
	}
	laptopSession := laptop.currentSession()
	if laptopSession.ID.String() != laptop.accessSID() {
		t.Fatalf("current session %s does not match the access token sid %s", laptopSession.ID, laptop.accessSID())
	}

	// Refresh rotates the token inside the same family.
	phoneSID := phone.accessSID()
	phone.expect(http.MethodPost, "/api/v1/auth/refresh", http.StatusOK, nil)
	if got := phone.accessSID(); got != phoneSID {
		t.Fatalf("refresh changed the session family: %s -> %s", phoneSID, got)
	}

	// A viewer (no write role) may end their own sessions; the laptop's
	// still-unexpired access JWT stops working on its next request.
	laptop.expect(http.MethodGet, "/api/v1/auth-context", http.StatusOK, nil)
	phone.expect(http.MethodDelete, "/api/v1/account/sessions/"+laptopSession.ID.String(), http.StatusNoContent, nil)
	laptop.expect(http.MethodGet, "/api/v1/auth-context", http.StatusUnauthorized, nil)
	laptop.expect(http.MethodGet, "/api/v1/auth/me", http.StatusUnauthorized, nil)
	laptop.expect(http.MethodPost, "/api/v1/auth/refresh", http.StatusUnauthorized, nil)
	phone.expect(http.MethodGet, "/api/v1/auth-context", http.StatusOK, nil)
	f.waitForAudit("auth.session_revoke", f.userID("alice"))

	// Revoke-others keeps the caller's session.
	tablet := f.signIn("alice")
	var revoked models.RevokeSessionsResponse
	phone.expect(http.MethodPost, "/api/v1/account/sessions/revoke-others", http.StatusOK, &revoked)
	if revoked.Revoked != 1 {
		t.Fatalf("revoke-others revoked %d, want 1", revoked.Revoked)
	}
	tablet.expect(http.MethodGet, "/api/v1/auth-context", http.StatusUnauthorized, nil)
	phone.expect(http.MethodGet, "/api/v1/auth-context", http.StatusOK, nil)

	// A superadmin signs the user out everywhere.
	root := f.signIn("root")
	root.expect(http.MethodDelete, "/api/v1/users/"+f.userID("alice").String()+"/sessions", http.StatusOK, &revoked)
	if revoked.Revoked != 1 {
		t.Fatalf("admin revoke revoked %d, want 1", revoked.Revoked)
	}
	phone.expect(http.MethodGet, "/api/v1/auth-context", http.StatusUnauthorized, nil)
	root.expect(http.MethodGet, "/api/v1/auth-context", http.StatusOK, nil)

	// Logout everywhere ends the caller's own session too.
	root2 := f.signIn("root")
	root.expect(http.MethodPost, "/api/v1/account/sessions/revoke-all", http.StatusOK, &revoked)
	if revoked.Revoked != 2 {
		t.Fatalf("revoke-all revoked %d, want 2", revoked.Revoked)
	}
	root2.expect(http.MethodGet, "/api/v1/auth-context", http.StatusUnauthorized, nil)

	// Account routes are for browser sessions only.
	req, _ := http.NewRequest(http.MethodGet, f.http.URL+"/api/v1/account/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+f.apiKey(f.tenantA, "write"))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("API key on /account/sessions: %d, want 403", res.StatusCode)
	}
}

func TestLoginLockoutAndUnlock(t *testing.T) {
	f := newMCPFixture(t)
	f.createUser("root", "superadmin", nil)
	f.createUser("alice", "member", nil)
	alice := f.userID("alice")
	anon := &http.Client{}

	login := func(username, password string) (int, string, string) {
		raw, _ := json.Marshal(map[string]string{"username": username, "password": password})
		res, err := anon.Post(f.http.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header.Get("Retry-After"), string(data)
	}

	var lockedBody, ghostBody string
	for _, username := range []string{"alice", "nobody"} {
		for i := 0; i < adminauth.LockoutThreshold; i++ {
			if status, _, body := login(username, "wrong-password"); status != http.StatusUnauthorized {
				t.Fatalf("%s attempt %d: %d %s", username, i+1, status, body)
			}
		}
		// Locked: even the right password gets the generic 429.
		status, retryAfter, body := login(username, identityPassword)
		if status != http.StatusTooManyRequests || retryAfter == "" {
			t.Fatalf("%s locked login: %d retry-after=%q %s", username, status, retryAfter, body)
		}
		if username == "alice" {
			lockedBody = body
		} else {
			ghostBody = body
		}
	}
	// A locked account and an unknown username answer identically.
	if lockedBody != ghostBody {
		t.Fatalf("lockout responses differ:\n%s\n%s", lockedBody, ghostBody)
	}
	f.waitForAudit("auth.lockout", alice)

	// The superadmin unlocks through the MCP tool.
	tokens := f.oauthSignIn("root", identityPassword, "write")
	session := f.connect(tokens["access_token"].(string))
	out, isErr := callTool(t, session, "unlock_user", map[string]any{"id": alice.String()})
	if isErr || !strings.Contains(out, `"was_locked":true`) {
		t.Fatalf("unlock_user: %v %s", isErr, out)
	}
	f.waitForAudit("auth.unlock", alice)
	if status, _, body := login("alice", identityPassword); status != http.StatusOK {
		t.Fatalf("login after unlock: %d %s", status, body)
	}

	// The REST route answers too, and 404s an unknown user.
	root := f.signIn("root")
	var unlocked models.UnlockUserResponse
	root.expect(http.MethodPost, "/api/v1/users/"+alice.String()+"/unlock", http.StatusOK, &unlocked)
	if unlocked.WasLocked {
		t.Fatal("was_locked for an unlocked account")
	}
	root.expect(http.MethodPost, "/api/v1/users/"+uuid.NewString()+"/unlock", http.StatusNotFound, nil)

	// Locked users show it in user management.
	for i := 0; i < adminauth.LockoutThreshold; i++ {
		login("alice", "wrong-password")
	}
	var user models.AdminUser
	root.expect(http.MethodGet, "/api/v1/users/"+alice.String(), http.StatusOK, &user)
	if user.LockedUntil == nil || time.Until(*user.LockedUntil) <= 0 {
		t.Fatalf("locked_until = %v, want a future time", user.LockedUntil)
	}
}

// Behind a proxy every user shares one client address: failures against
// other usernames from it, however many, must not block anyone else's
// sign-in.
func TestFailedLoginsFromSharedAddressDoNotBlockOthers(t *testing.T) {
	f := newMCPFixture(t)
	f.createUser("alice", "member", nil)

	for i := 0; i < 60; i++ {
		raw, _ := json.Marshal(map[string]string{"username": "spray-" + strconv.Itoa(i), "password": "wrong-password"})
		res, err := http.Post(f.http.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("spray attempt %d: %d, want 401", i+1, res.StatusCode)
		}
	}
	f.signIn("alice")
}
