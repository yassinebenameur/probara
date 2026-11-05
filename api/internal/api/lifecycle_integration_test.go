//go:build !unit

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	adminusersservice "github.com/yassinebenameur/probara/api/internal/services/adminusers"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// outbox captures account mail.
type outbox struct {
	mu   sync.Mutex
	msgs []mail.Message
}

func (o *outbox) SendMessage(_ context.Context, msg mail.Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.msgs = append(o.msgs, msg)
	return nil
}

func (o *outbox) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.msgs)
}

// waitFor returns the first message to `to` whose subject contains subject.
func (o *outbox) waitFor(t *testing.T, to, subject string) mail.Message {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		o.mu.Lock()
		for _, m := range o.msgs {
			if m.To[0] == to && strings.Contains(m.Subject, subject) {
				o.mu.Unlock()
				return m
			}
		}
		o.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no %q mail to %s", subject, to)
	return mail.Message{}
}

// tokenFrom extracts the #token= fragment of a link in s.
func tokenFrom(t *testing.T, s string) string {
	t.Helper()
	i := strings.Index(s, "#token=")
	if i < 0 {
		t.Fatalf("no token link in %q", s)
	}
	rest := s[i+len("#token="):]
	if j := strings.IndexAny(rest, " \n\"<"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// newIdentityFixture is newMCPFixture with a config hook and an account
// mail outbox (nil leaves mail off).
func newIdentityFixture(t *testing.T, box *outbox, mutate func(*config.APIConfig)) *mcpFixture {
	t.Helper()
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	cfg := &config.APIConfig{}
	cfg.ServiceName = "api-test"
	cfg.NATSURL = "nats://127.0.0.1:1"
	cfg.AdminJWTSecret = "identity-test-secret-identity-test"
	cfg.AdminAccessTTLMinutes = 15
	cfg.AdminRefreshTTLDays = 30
	cfg.AdminBcryptCost = 4
	if mutate != nil {
		mutate(cfg)
	}
	if box != nil {
		identityMailerOverride = box
		t.Cleanup(func() { identityMailerOverride = nil })
	}
	s := NewServer(cfg, logger.New("api-test", "error"), metrics.NewRegistry("api-test"), dbClient)
	if s.mcpErr != nil {
		t.Fatal(s.mcpErr)
	}
	s.auditRecorder.Start()
	t.Cleanup(s.auditRecorder.Stop)
	srv := httptest.NewServer(s.http.Handler)
	t.Cleanup(srv.Close)
	return &mcpFixture{
		t: t, ctx: ctx, db: dbClient, server: s, http: srv,
		tenantA: testutil.InsertTenant(ctx, t, dbClient, "Tenant A"),
		tenantB: testutil.InsertTenant(ctx, t, dbClient, "Tenant B"),
	}
}

func (f *mcpFixture) anon() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{f: f, client: &http.Client{Jar: jar}}
}

// call sends a JSON body and checks the status.
func (b *browser) call(method, path string, body any, want int, out any) {
	b.f.t.Helper()
	status, data := b.do(method, path, body)
	if status != want {
		b.f.t.Fatalf("%s %s: want %d, got %d: %s", method, path, want, status, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			b.f.t.Fatalf("%s %s: %v: %s", method, path, err, data)
		}
	}
}

// login signs in with a password; it returns the browser and the raw body.
func (f *mcpFixture) login(username, password string) (*browser, map[string]any) {
	f.t.Helper()
	b := f.anon()
	var out map[string]any
	b.call(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": username, "password": password}, http.StatusOK, &out)
	return b, out
}

func (f *mcpFixture) createUserWith(username string, password *string, email string) {
	f.t.Helper()
	req := &models.CreateAdminUserRequest{Username: username, Password: password, PlatformRole: "member",
		Memberships: []models.MembershipInput{{TenantID: f.tenantA, Role: "viewer"}}}
	if email != "" {
		req.Email = &email
	}
	if _, err := adminusersservice.NewService(f.db, 4).CreateUser(f.ctx, req); err != nil {
		f.t.Fatal(err)
	}
}

func TestInviteResetAndPasswordChangeWithoutSMTP(t *testing.T) {
	f := newIdentityFixture(t, nil, nil)
	f.createUser("root", "superadmin", nil)
	f.createUserWith("newbie", nil, "newbie@example.com")
	newbie := f.userID("newbie")
	root := f.signIn("root")
	anon := f.anon()

	var caps models.AuthCapabilities
	anon.call(http.MethodGet, "/api/v1/auth/capabilities", nil, http.StatusOK, &caps)
	if caps.PasswordReset || caps.EmailDelivery || caps.OIDC {
		t.Fatalf("capabilities without SMTP = %+v", caps)
	}

	// Without SMTP the invite comes back as a link to hand over.
	var link models.AccountLinkResponse
	root.call(http.MethodPost, "/api/v1/users/"+newbie.String()+"/invite", map[string]any{}, http.StatusOK, &link)
	if link.Delivered || link.Method != "password" || !strings.HasPrefix(link.Path, "/accept-invite#token=") || link.URL != "" {
		t.Fatalf("invite = %+v", link)
	}
	token := tokenFrom(t, link.Path)
	var info models.InviteInfo
	anon.call(http.MethodPost, "/api/v1/auth/invite/inspect", map[string]any{"token": token}, http.StatusOK, &info)
	if info.Username != "newbie" {
		t.Fatalf("invite info = %+v", info)
	}
	anon.call(http.MethodPost, "/api/v1/auth/invite/accept", map[string]any{"token": token, "password": "short"}, http.StatusBadRequest, nil)
	anon.call(http.MethodPost, "/api/v1/auth/invite/accept", map[string]any{"token": token, "password": identityPassword}, http.StatusNoContent, nil)
	anon.call(http.MethodPost, "/api/v1/auth/invite/accept", map[string]any{"token": token, "password": identityPassword}, http.StatusBadRequest, nil)
	f.waitForAudit("auth.invite_accept", newbie)

	laptop, _ := f.login("newbie", identityPassword)
	laptop.expect(http.MethodGet, "/api/v1/auth-context", http.StatusOK, nil)

	// Forgot-password always answers 202, for real and unknown names alike.
	anon.call(http.MethodPost, "/api/v1/auth/password/forgot", map[string]any{"login": "newbie"}, http.StatusAccepted, nil)
	anon.call(http.MethodPost, "/api/v1/auth/password/forgot", map[string]any{"login": "nobody"}, http.StatusAccepted, nil)

	// A superadmin-issued reset link, handed over, signs the user out.
	root.call(http.MethodPost, "/api/v1/users/"+newbie.String()+"/password-reset", nil, http.StatusOK, &link)
	if link.Delivered || !strings.HasPrefix(link.Path, "/reset-password#token=") {
		t.Fatalf("reset = %+v", link)
	}
	newPassword := identityPassword + "-2"
	anon.call(http.MethodPost, "/api/v1/auth/password/reset", map[string]any{"token": tokenFrom(t, link.Path), "password": newPassword}, http.StatusNoContent, nil)
	laptop.expect(http.MethodGet, "/api/v1/auth-context", http.StatusUnauthorized, nil)
	f.waitForAudit("auth.password_reset", newbie)

	// Self-service password change keeps this session, ends the others.
	desk, _ := f.login("newbie", newPassword)
	phone, _ := f.login("newbie", newPassword)
	var revoked models.RevokeSessionsResponse
	desk.call(http.MethodPatch, "/api/v1/account/password", map[string]any{"current_password": "wrong-password-xx", "new_password": identityPassword}, http.StatusBadRequest, nil)
	desk.call(http.MethodPatch, "/api/v1/account/password", map[string]any{"current_password": newPassword, "new_password": "short"}, http.StatusBadRequest, nil)
	desk.call(http.MethodPatch, "/api/v1/account/password", map[string]any{"current_password": newPassword, "new_password": identityPassword}, http.StatusOK, &revoked)
	if revoked.Revoked != 1 {
		t.Fatalf("password change revoked %d sessions, want 1", revoked.Revoked)
	}
	desk.expect(http.MethodGet, "/api/v1/auth-context", http.StatusOK, nil)
	phone.expect(http.MethodGet, "/api/v1/auth-context", http.StatusUnauthorized, nil)

	// No SMTP: the new address cannot be confirmed.
	desk.call(http.MethodPost, "/api/v1/account/email", map[string]any{"new_email": "n@example.com", "current_password": identityPassword}, http.StatusServiceUnavailable, nil)

	// An IdP-linked account changes nothing itself and gets no password link.
	if _, err := f.db.ExecContext(f.ctx, `UPDATE admin_users SET external_subject = 'sub-newbie' WHERE id = $1`, newbie); err != nil {
		t.Fatal(err)
	}
	desk.call(http.MethodPatch, "/api/v1/account/password", map[string]any{"current_password": identityPassword, "new_password": newPassword}, http.StatusConflict, nil)
	desk.call(http.MethodPost, "/api/v1/account/2fa/setup", map[string]any{"current_password": identityPassword}, http.StatusConflict, nil)
	root.call(http.MethodPost, "/api/v1/users/"+newbie.String()+"/invite", map[string]any{"method": "password"}, http.StatusConflict, nil)
	root.call(http.MethodPost, "/api/v1/users/"+newbie.String()+"/password-reset", nil, http.StatusConflict, nil)
	var mfa models.AccountMFAStatus
	desk.call(http.MethodGet, "/api/v1/account/2fa", nil, http.StatusOK, &mfa)
	if mfa.Eligible || !mfa.Available {
		t.Fatalf("SSO account 2fa status = %+v", mfa)
	}

	// Account routes refuse API keys.
	req, _ := http.NewRequest(http.MethodPatch, f.http.URL+"/api/v1/account/password", bytes.NewBufferString(`{}`))
	req.Header.Set("Authorization", "Bearer "+f.apiKey(f.tenantA, "write"))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("API key on /account/password: %d, want 403", res.StatusCode)
	}
}

func TestAccountMailFlows(t *testing.T) {
	box := &outbox{}
	f := newIdentityFixture(t, box, func(cfg *config.APIConfig) { cfg.AppBaseURL = "https://ops.example.com" })
	f.createUser("root", "superadmin", nil)
	pw := identityPassword
	f.createUserWith("alice", &pw, "alice@example.com")
	alice := f.userID("alice")
	anon := f.anon()

	var caps models.AuthCapabilities
	anon.call(http.MethodGet, "/api/v1/auth/capabilities", nil, http.StatusOK, &caps)
	if !caps.PasswordReset || !caps.EmailDelivery {
		t.Fatalf("capabilities with SMTP = %+v", caps)
	}

	// Unknown login: 202 and no mail. Known email (any case): a reset link.
	session, _ := f.login("alice", identityPassword)
	anon.call(http.MethodPost, "/api/v1/auth/password/forgot", map[string]any{"login": "ghost@example.com"}, http.StatusAccepted, nil)
	anon.call(http.MethodPost, "/api/v1/auth/password/forgot", map[string]any{"login": "ALICE@example.com"}, http.StatusAccepted, nil)
	msg := box.waitFor(t, "alice@example.com", "Reset")
	if !strings.Contains(msg.Text, "https://ops.example.com/reset-password#token=") {
		t.Fatalf("reset mail: %s", msg.Text)
	}
	if box.count() != 1 {
		t.Fatalf("%d mails, want 1 (none for the unknown login)", box.count())
	}
	f.waitForAudit("auth.password_reset_request", alice)
	newPassword := identityPassword + "-reset"
	anon.call(http.MethodPost, "/api/v1/auth/password/reset", map[string]any{"token": tokenFrom(t, msg.Text), "password": newPassword}, http.StatusNoContent, nil)
	session.expect(http.MethodGet, "/api/v1/auth-context", http.StatusUnauthorized, nil)

	// Email change: confirmed through the new address, the old one is told.
	session, _ = f.login("alice", newPassword)
	var pending models.EmailChangePendingResponse
	session.call(http.MethodPost, "/api/v1/account/email", map[string]any{"new_email": "alice@new.example", "current_password": "nope-nope-nope"}, http.StatusBadRequest, nil)
	session.call(http.MethodPost, "/api/v1/account/email", map[string]any{"new_email": "Alice@New.Example", "current_password": newPassword}, http.StatusAccepted, &pending)
	if pending.PendingEmail != "alice@new.example" {
		t.Fatalf("pending = %+v", pending)
	}
	confirm := box.waitFor(t, "alice@new.example", "Confirm")
	var confirmed models.EmailConfirmedResponse
	anon.call(http.MethodPost, "/api/v1/auth/email/confirm", map[string]any{"token": tokenFrom(t, confirm.Text)}, http.StatusOK, &confirmed)
	if confirmed.Email != "alice@new.example" {
		t.Fatalf("confirmed = %+v", confirmed)
	}
	box.waitFor(t, "alice@example.com", "was changed")
	f.waitForAudit("auth.email_change", alice)

	// With SMTP the invite is mailed and no link comes back.
	root := f.signIn("root")
	var link models.AccountLinkResponse
	root.call(http.MethodPost, "/api/v1/users/"+alice.String()+"/invite", nil, http.StatusOK, &link)
	if !link.Delivered || link.Path != "" || link.URL != "" {
		t.Fatalf("invite with SMTP = %+v", link)
	}
	box.waitFor(t, "alice@new.example", "invited")
}

func TestTOTPSignIn(t *testing.T) {
	f := newIdentityFixture(t, nil, nil)
	f.createUser("root", "superadmin", nil)
	pw := identityPassword
	f.createUserWith("bob", &pw, "")
	bob := f.userID("bob")

	session, _ := f.login("bob", identityPassword)
	var st models.AccountMFAStatus
	session.call(http.MethodGet, "/api/v1/account/2fa", nil, http.StatusOK, &st)
	if !st.Available || !st.Eligible || st.Enabled {
		t.Fatalf("status = %+v", st)
	}
	session.call(http.MethodPost, "/api/v1/account/2fa/setup", map[string]any{"current_password": "wrong-password-x"}, http.StatusBadRequest, nil)
	var setup models.TOTPSetupResponse
	session.call(http.MethodPost, "/api/v1/account/2fa/setup", map[string]any{"current_password": identityPassword}, http.StatusOK, &setup)
	if setup.Secret == "" || !strings.HasPrefix(setup.OTPAuthURI, "otpauth://totp/Probara:bob?") {
		t.Fatalf("setup = %+v", setup)
	}
	session.call(http.MethodPost, "/api/v1/account/2fa/enable", map[string]any{"code": "123"}, http.StatusBadRequest, nil)
	prev, _ := adminauth.TOTPCodeAt(setup.Secret, time.Now().Add(-30*time.Second))
	var codes models.RecoveryCodesResponse
	session.call(http.MethodPost, "/api/v1/account/2fa/enable", map[string]any{"code": prev}, http.StatusOK, &codes)
	if len(codes.RecoveryCodes) != adminauth.RecoveryCodeCount {
		t.Fatalf("recovery codes = %v", codes.RecoveryCodes)
	}
	f.waitForAudit("auth.mfa_enable", bob)

	// The password alone now yields a step-one token and no session.
	b, out := f.login("bob", identityPassword)
	if out["mfa_required"] != true || out["mfa_token"] == "" || out["user"] != nil {
		t.Fatalf("login = %v", out)
	}
	b.expect(http.MethodGet, "/api/v1/auth/me", http.StatusUnauthorized, nil)
	mfaToken := out["mfa_token"].(string)
	b.call(http.MethodPost, "/api/v1/auth/login/mfa", map[string]any{"mfa_token": "garbage", "code": "000000"}, http.StatusUnauthorized, nil)
	b.call(http.MethodPost, "/api/v1/auth/login/mfa", map[string]any{"mfa_token": mfaToken, "code": "000000"}, http.StatusUnauthorized, nil)
	code, _ := adminauth.TOTPCodeAt(setup.Secret, time.Now())
	var signedIn models.AdminAuthResponse
	b.call(http.MethodPost, "/api/v1/auth/login/mfa", map[string]any{"mfa_token": mfaToken, "code": code}, http.StatusOK, &signedIn)
	if signedIn.User.ID != bob {
		t.Fatalf("signed in as %+v", signedIn.User)
	}
	b.expect(http.MethodGet, "/api/v1/auth-context", http.StatusOK, nil)

	// The same code never works twice.
	b2, out2 := f.login("bob", identityPassword)
	b2.call(http.MethodPost, "/api/v1/auth/login/mfa", map[string]any{"mfa_token": out2["mfa_token"], "code": code}, http.StatusUnauthorized, nil)
	// A recovery code does, once.
	b2.call(http.MethodPost, "/api/v1/auth/login/mfa", map[string]any{"mfa_token": out2["mfa_token"], "code": codes.RecoveryCodes[0]}, http.StatusOK, nil)

	// The step-one token is not a session cookie.
	req, _ := http.NewRequest(http.MethodGet, f.http.URL+"/api/v1/auth-context", nil)
	req.AddCookie(&http.Cookie{Name: "admin_access", Value: mfaToken})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("step-one token as a cookie: %d", res.StatusCode)
	}

	// Superadmin reset: the password alone signs in again.
	root := f.signIn("root")
	var reset models.ResetMFAResponse
	root.call(http.MethodPost, "/api/v1/users/"+bob.String()+"/reset-2fa", nil, http.StatusOK, &reset)
	if !reset.WasEnabled {
		t.Fatal("reset-2fa reported 2FA off")
	}
	f.waitForAudit("auth.mfa_reset", bob)
	_, out3 := f.login("bob", identityPassword)
	if out3["user"] == nil {
		t.Fatalf("login after reset = %v", out3)
	}
	var user models.AdminUser
	root.call(http.MethodGet, "/api/v1/users/"+bob.String(), nil, http.StatusOK, &user)
	if user.MFAEnabled {
		t.Fatal("user still shows 2FA after reset")
	}
}
