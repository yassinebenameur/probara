//go:build !unit

package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// mailSink is a minimal SMTP server that accepts everything and keeps each
// message's DATA.
type mailSink struct {
	port int
	mu   sync.Mutex
	data []string
}

func startMailSink(t *testing.T) *mailSink {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &mailSink{port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
				write("220 sink ESMTP")
				var body strings.Builder
				inData := false
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if inData {
						if strings.TrimRight(line, "\r\n") == "." {
							inData = false
							s.mu.Lock()
							s.data = append(s.data, body.String())
							s.mu.Unlock()
							body.Reset()
							write("250 queued")
							continue
						}
						body.WriteString(strings.TrimPrefix(line, "."))
						continue
					}
					switch strings.ToUpper(strings.Fields(line + " x")[0]) {
					case "DATA":
						inData = true
						write("354 go ahead")
					case "QUIT":
						write("221 bye")
						return
					default:
						write("250 ok")
					}
				}
			}()
		}
	}()
	return s
}

func (s *mailSink) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.data...)
}

// mailsTo returns the messages addressed to rcpt, waiting up to wait for at
// least want of them.
func (s *mailSink) mailsTo(rcpt string, want int, wait time.Duration) []string {
	deadline := time.Now().Add(wait)
	for {
		var out []string
		for _, m := range s.messages() {
			if strings.Contains(m, "To: <"+rcpt+">") {
				out = append(out, m)
			}
		}
		if len(out) >= want || time.Now().After(deadline) {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// mailText decodes a raw message's plain-text part (the mailer sends every
// part base64-encoded).
func mailText(t *testing.T, raw string) string {
	t.Helper()
	msg, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var walk func(contentType string, body io.Reader, encoding string) string
	walk = func(contentType string, body io.Reader, encoding string) string {
		mediaType, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(mediaType, "multipart/") {
			mr := multipart.NewReader(body, params["boundary"])
			for {
				part, err := mr.NextPart()
				if err != nil {
					return ""
				}
				if text := walk(part.Header.Get("Content-Type"), part, part.Header.Get("Content-Transfer-Encoding")); text != "" {
					return text
				}
			}
		}
		if mediaType != "text/plain" {
			return ""
		}
		if strings.EqualFold(encoding, "base64") {
			body = base64.NewDecoder(base64.StdEncoding, body)
		}
		data, _ := io.ReadAll(body)
		return string(data)
	}
	return walk(msg.Header.Get("Content-Type"), msg.Body, msg.Header.Get("Content-Transfer-Encoding"))
}

// tokenLink extracts the token of the first link to page in a raw message.
func tokenLink(t *testing.T, raw, page string) string {
	t.Helper()
	text := mailText(t, raw)
	m := regexp.MustCompile(regexp.QuoteMeta(page) + `#([A-Za-z0-9_-]{43})`).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no %s link in:\n%s", page, text)
	}
	return m[1]
}

// newAccountFixture is newMCPFixture with account mail (an SMTP sink and
// APP_BASE_URL) when sink is set, and PROBARA_SECRETS_KEY set so two-factor
// enrolment works.
func newAccountFixture(t *testing.T, sink *mailSink, configure ...func(*config.APIConfig)) *mcpFixture {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	t.Setenv("PROBARA_SECRETS_KEY", base64.StdEncoding.EncodeToString(key))
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	cfg := &config.APIConfig{}
	cfg.ServiceName = "api-test"
	cfg.NATSURL = "nats://127.0.0.1:1"
	cfg.AdminJWTSecret = "mcp-test-secret-mcp-test-secret-32b"
	cfg.AdminAccessTTLMinutes = 15
	cfg.AdminRefreshTTLDays = 30
	cfg.AdminBcryptCost = 4
	if sink != nil {
		cfg.SMTPHost = "127.0.0.1"
		cfg.SMTPPort = sink.port
		cfg.SMTPFrom = "probara@example.com"
		cfg.AppBaseURL = "https://app.example.com"
	}
	for _, fn := range configure {
		fn(cfg)
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

// doToken sends a request carrying a link token in the X-Action-Token header.
func (b *browser) doToken(method, path, token string, body any) (int, []byte) {
	b.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, b.f.http.URL+path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Action-Token", token)
	res, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

func decodeJSON[T any](t *testing.T, status int, body []byte, want int) T {
	t.Helper()
	var out T
	if status != want {
		t.Fatalf("status %d, want %d: %s", status, want, body)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

// nextCode is the code for the step after userID's last accepted one.
func (f *mcpFixture) nextCode(secret string, userID uuid.UUID) string {
	f.t.Helper()
	var last int64
	if err := f.db.QueryRowContext(f.ctx, `SELECT totp_last_step FROM admin_users WHERE id = $1`, userID).Scan(&last); err != nil {
		f.t.Fatal(err)
	}
	code, err := adminauth.TOTPCodeAt(secret, time.Unix((last+1)*30, 0))
	if err != nil {
		f.t.Fatal(err)
	}
	return code
}

func TestTwoFactorSignInOverHTTP(t *testing.T) {
	f := newAccountFixture(t, nil)
	f.createUser("alice", "member", nil)
	alice := f.userID("alice")
	laptop := f.browser().mustLogin("alice")

	// Enrol: the password first, then a code confirms it.
	status, body := laptop.do(http.MethodPost, "/api/v1/account/two-factor/setup", map[string]string{"current_password": "nope-nope-nope"})
	if status != http.StatusBadRequest || !strings.Contains(string(body), "invalid_password") {
		t.Fatalf("setup with a wrong password: %d %s", status, body)
	}
	status, body = laptop.do(http.MethodPost, "/api/v1/account/two-factor/setup", map[string]string{"current_password": identityPassword})
	setup := decodeJSON[models.TOTPSetupResponse](t, status, body, http.StatusOK)
	if !strings.HasPrefix(setup.OTPAuthURI, "otpauth://totp/Probara:alice?") || setup.Secret == "" {
		t.Fatalf("setup: %+v", setup)
	}
	code, _ := adminauth.TOTPCodeAt(setup.Secret, time.Now())
	status, body = laptop.do(http.MethodPost, "/api/v1/account/two-factor/enable", map[string]string{"code": code})
	enabled := decodeJSON[models.RecoveryCodesResponse](t, status, body, http.StatusOK)
	if len(enabled.RecoveryCodes) != adminauth.RecoveryCodeCount {
		t.Fatalf("recovery codes: %v", enabled.RecoveryCodes)
	}
	// Enabling keeps the session that did it.
	if status := laptop.status(http.MethodGet, "/api/v1/auth/me"); status != http.StatusOK {
		t.Fatalf("enroller signed out: %d", status)
	}

	// The password step sets no cookie and answers with a challenge only.
	phone := f.browser()
	status, body = phone.login("alice", identityPassword)
	challenge := decodeJSON[models.MFAChallengeResponse](t, status, body, http.StatusOK)
	if !challenge.MFARequired || challenge.MFAToken == "" || strings.Contains(string(body), `"user"`) {
		t.Fatalf("password step: %s", body)
	}
	if phone.cookie("admin_access") != "" || phone.cookie("admin_refresh") != "" {
		t.Fatal("a session cookie was set before the second step")
	}
	if status := phone.status(http.MethodGet, "/api/v1/auth/me"); status != http.StatusUnauthorized {
		t.Fatalf("/auth/me between the steps: %d", status)
	}
	// A wrong password on the same account is the ordinary refusal.
	wrongPwStatus, wrongPwBody := f.browser().login("alice", "wrong-password-guess")
	unknownStatus, unknownBody := f.browser().login("nobody-at-all", "wrong-password-guess")
	if wrongPwStatus != http.StatusUnauthorized || unknownStatus != wrongPwStatus || !bytes.Equal(wrongPwBody, unknownBody) {
		t.Fatalf("wrong password on a two-factor account: %d %s vs %d %s", wrongPwStatus, wrongPwBody, unknownStatus, unknownBody)
	}

	// A wrong code: 401, no cookie.
	status, wrongCodeBody := phone.do(http.MethodPost, "/api/v1/auth/login/mfa", map[string]string{"mfa_token": challenge.MFAToken, "code": "000001"})
	if status != http.StatusUnauthorized || phone.cookie("admin_access") != "" {
		t.Fatalf("wrong code: %d %s", status, wrongCodeBody)
	}
	// The right code: a session marked with its second factor.
	status, body = phone.do(http.MethodPost, "/api/v1/auth/login/mfa", map[string]string{"mfa_token": challenge.MFAToken, "code": f.nextCode(setup.Secret, alice)})
	if status != http.StatusOK || phone.cookie("admin_access") == "" {
		t.Fatalf("right code: %d %s", status, body)
	}
	var marked bool
	for _, s := range phone.sessions() {
		if s.Current && s.SecondFactor == adminauth.SecondFactorTOTP {
			marked = true
		}
	}
	if !marked {
		t.Fatal("the session does not show its second factor")
	}
	f.waitAudit("auth.login", alice.String())
	// The challenge is used up.
	status, body = f.browser().do(http.MethodPost, "/api/v1/auth/login/mfa", map[string]string{"mfa_token": challenge.MFAToken, "code": enabled.RecoveryCodes[0]})
	if status != http.StatusUnauthorized || !strings.Contains(string(body), "mfa_challenge_invalid") {
		t.Fatalf("reused challenge: %d %s", status, body)
	}

	// Ten wrong codes lock the account; then the right code gets the same
	// answer as a wrong one.
	tablet := f.browser()
	status, body = tablet.login("alice", identityPassword)
	second := decodeJSON[models.MFAChallengeResponse](t, status, body, http.StatusOK)
	for i := 0; i < adminauth.LockoutThreshold; i++ {
		tablet.do(http.MethodPost, "/api/v1/auth/login/mfa", map[string]string{"mfa_token": second.MFAToken, "code": "000001"})
	}
	f.waitAudit("auth.second_factor_lockout", alice.String())
	status, lockedBody := tablet.do(http.MethodPost, "/api/v1/auth/login/mfa", map[string]string{"mfa_token": second.MFAToken, "code": enabled.RecoveryCodes[1]})
	if status != http.StatusUnauthorized || !bytes.Equal(lockedBody, wrongCodeBody) {
		t.Fatalf("right code while locked: %d %s, want the wrong-code answer %s", status, lockedBody, wrongCodeBody)
	}

	// Turning it off needs the password and a code, and signs out the other
	// sessions.
	status, body = laptop.do(http.MethodPost, "/api/v1/account/two-factor/disable", map[string]string{"current_password": identityPassword, "code": "000001"})
	if status != http.StatusTooManyRequests || !strings.Contains(string(body), "account_locked") {
		t.Fatalf("disable while locked: %d %s", status, body)
	}
	root := f.createSuperadminBrowser()
	// The superadmin sees that wrong codes locked the account.
	status, body = root.do(http.MethodGet, "/api/v1/users/"+alice.String(), nil)
	if detail := decodeJSON[models.AdminUser](t, status, body, http.StatusOK); detail.SecondFactorLockouts != 1 || detail.LastSecondFactorLockoutAt == nil || detail.LockoutHeld {
		t.Fatalf("user detail after a second-factor lockout: %s", body)
	}
	if status := root.status(http.MethodPost, "/api/v1/users/"+alice.String()+"/unlock"); status != http.StatusOK {
		t.Fatalf("unlock: %d", status)
	}
	status, body = laptop.do(http.MethodPost, "/api/v1/account/two-factor/disable", map[string]string{"current_password": identityPassword, "code": "000001"})
	if status != http.StatusBadRequest || !strings.Contains(string(body), "invalid_code") {
		t.Fatalf("disable with a wrong code: %d %s", status, body)
	}
	status, body = laptop.do(http.MethodPost, "/api/v1/account/two-factor/disable", map[string]string{"current_password": identityPassword, "code": enabled.RecoveryCodes[2]})
	if status != http.StatusOK || !strings.Contains(string(body), `"revoked":1`) {
		t.Fatalf("disable: %d %s", status, body)
	}
	if status := phone.status(http.MethodGet, "/api/v1/account/sessions"); status != http.StatusUnauthorized {
		t.Fatalf("other session after disable: %d, want signed out", status)
	}
	f.waitAudit("auth.two_factor_disable", alice.String())
	if status, body := f.browser().login("alice", identityPassword); status != http.StatusOK || !strings.Contains(string(body), `"user"`) {
		t.Fatalf("password sign-in after disable: %d %s", status, body)
	}
}

// createSuperadminBrowser creates a superadmin "root" and signs it in.
func (f *mcpFixture) createSuperadminBrowser() *browser {
	f.t.Helper()
	f.createUser("root", "superadmin", nil)
	return f.browser().mustLogin("root")
}

func TestSuperadminTwoFactorResetOverHTTP(t *testing.T) {
	f := newAccountFixture(t, nil)
	f.createUser("alice", "member", nil)
	alice := f.userID("alice")
	laptop := f.browser().mustLogin("alice")
	status, body := laptop.do(http.MethodPost, "/api/v1/account/two-factor/setup", map[string]string{"current_password": identityPassword})
	setup := decodeJSON[models.TOTPSetupResponse](t, status, body, http.StatusOK)
	code, _ := adminauth.TOTPCodeAt(setup.Secret, time.Now())
	if status, body := laptop.do(http.MethodPost, "/api/v1/account/two-factor/enable", map[string]string{"code": code}); status != http.StatusOK {
		t.Fatalf("enable: %d %s", status, body)
	}

	bob := f.browser()
	f.createUser("bob", "member", nil)
	bob.mustLogin("bob")
	if status := bob.status(http.MethodPost, "/api/v1/users/"+alice.String()+"/reset-2fa"); status != http.StatusForbidden {
		t.Fatalf("member reset: %d, want 403", status)
	}
	root := f.createSuperadminBrowser()
	status, body = root.do(http.MethodGet, "/api/v1/users/"+alice.String(), nil)
	user := decodeJSON[models.AdminUser](t, status, body, http.StatusOK)
	if !user.TwoFactorEnabled || user.HasPassword == nil || !*user.HasPassword {
		t.Fatalf("user detail: %s", body)
	}
	status, body = root.do(http.MethodPost, "/api/v1/users/"+alice.String()+"/reset-2fa", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"was_enabled":true`) {
		t.Fatalf("reset: %d %s", status, body)
	}
	f.waitAudit("user.two_factor_reset", alice.String())
	if status, body := f.browser().login("alice", identityPassword); status != http.StatusOK || !strings.Contains(string(body), `"user"`) {
		t.Fatalf("sign-in after reset: %d %s", status, body)
	}
	if status := laptop.status(http.MethodGet, "/api/v1/account/security"); status != http.StatusOK {
		t.Fatalf("the user's session after a reset: %d, want kept", status)
	}
}

// Without email delivery: superadmins get the link to hand over, members get
// nothing, and the public form sends nothing yet answers alike.
func TestAccountLinksWithoutEmailDelivery(t *testing.T) {
	f := newAccountFixture(t, nil)
	root := f.createSuperadminBrowser()
	f.createUser("bob", "member", nil)
	status, body := root.do(http.MethodPost, "/api/v1/users", models.CreateAdminUserRequest{
		Username: "newcomer", Email: ptrTo("newcomer@example.com"), PlatformRole: "member",
	})
	created := decodeJSON[models.AdminUser](t, status, body, http.StatusCreated)

	anon := f.browser()
	status, body = anon.do(http.MethodGet, "/api/v1/auth/capabilities", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"email_delivery":false`) {
		t.Fatalf("capabilities: %d %s", status, body)
	}

	bob := f.browser().mustLogin("bob")
	if status := bob.status(http.MethodPost, "/api/v1/users/"+created.ID.String()+"/invite"); status != http.StatusForbidden {
		t.Fatalf("member invite: %d, want 403", status)
	}
	status, body = root.do(http.MethodPost, "/api/v1/users/"+created.ID.String()+"/invite", nil)
	invite := decodeJSON[models.AccountLinkResponse](t, status, body, http.StatusOK)
	if invite.Delivered || invite.Method != "password" || !strings.HasPrefix(invite.Link, "/accept-invite#") || invite.Reason == "" || invite.ExpiresAt == nil {
		t.Fatalf("invite without SMTP: %+v", invite)
	}
	token := strings.TrimPrefix(invite.Link, "/accept-invite#")
	f.waitAudit("user.invite", created.ID.String())
	var details string
	_ = f.db.QueryRowContext(f.ctx, `SELECT details::text FROM audit_log WHERE action = 'user.invite'`).Scan(&details)
	if strings.Contains(details, token) {
		t.Fatal("the audit log holds the invite token")
	}

	status, body = anon.doToken(http.MethodGet, "/api/v1/auth/invite", token, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"username":"newcomer"`) {
		t.Fatalf("invite info: %d %s", status, body)
	}
	if status, body := anon.doToken(http.MethodPost, "/api/v1/auth/invite/accept", token, map[string]string{"password": "short"}); status != http.StatusBadRequest {
		t.Fatalf("short password: %d %s", status, body)
	}
	// An invite token is not a reset token.
	if status, _ := anon.doToken(http.MethodPost, "/api/v1/auth/password/reset", token, map[string]string{"password": "a-good-long-password"}); status != http.StatusBadRequest {
		t.Fatalf("invite token on the reset route: %d", status)
	}
	if status, body := anon.doToken(http.MethodPost, "/api/v1/auth/invite/accept", token, map[string]string{"password": "a-good-long-password"}); status != http.StatusNoContent {
		t.Fatalf("accept: %d %s", status, body)
	}
	if status, _ := anon.doToken(http.MethodPost, "/api/v1/auth/invite/accept", token, map[string]string{"password": "another-long-password"}); status != http.StatusBadRequest {
		t.Fatalf("second accept: %d", status)
	}
	if status, body := f.browser().login("newcomer", "a-good-long-password"); status != http.StatusOK {
		t.Fatalf("sign-in after accepting: %d %s", status, body)
	}

	// A reset link, returned to the superadmin.
	status, body = root.do(http.MethodPost, "/api/v1/users/"+created.ID.String()+"/password-reset", nil)
	reset := decodeJSON[models.AccountLinkResponse](t, status, body, http.StatusOK)
	if reset.Delivered || !strings.HasPrefix(reset.Link, "/reset-password#") {
		t.Fatalf("reset without SMTP: %+v", reset)
	}
	if status, body := anon.doToken(http.MethodPost, "/api/v1/auth/password/reset", strings.TrimPrefix(reset.Link, "/reset-password#"),
		map[string]string{"password": "yet-another-password"}); status != http.StatusNoContent {
		t.Fatalf("reset: %d %s", status, body)
	}

	// The public form answers alike and mails nothing.
	status, body = anon.do(http.MethodPost, "/api/v1/auth/password/forgot", map[string]string{"login": "newcomer"})
	if status != http.StatusAccepted || string(body) != `{"status":"accepted"}`+"\n" && string(body) != `{"status":"accepted"}` {
		t.Fatalf("forgot without SMTP: %d %q", status, body)
	}
	// The email change needs delivery.
	status, body = bob.do(http.MethodPost, "/api/v1/account/email", map[string]string{"current_password": identityPassword, "new_email": "bob@example.com"})
	if status != http.StatusConflict || !strings.Contains(string(body), "email_unavailable") {
		t.Fatalf("email change without SMTP: %d %s", status, body)
	}
}

func ptrTo[T any](v T) *T { return &v }

// The forgot-password route answers every case alike, mails only an
// eligible account (a locked one included), and caps the mails per account.
func TestForgotPasswordAnswersAlikeAndCapsMail(t *testing.T) {
	sink := startMailSink(t)
	f := newAccountFixture(t, sink)
	for _, u := range []string{"valid", "locked", "disabled", "sso"} {
		f.createUser(u, "member", nil)
		if _, err := f.db.ExecContext(f.ctx, `UPDATE admin_users SET email = $2 WHERE username = $1`, u, u+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE admin_users SET locked_until = NOW() + INTERVAL '10 minutes' WHERE username = 'locked'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE admin_users SET disabled_at = NOW() WHERE username = 'disabled'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE admin_users SET external_issuer = 'https://idp', external_subject = 's1' WHERE username = 'sso'`); err != nil {
		t.Fatal(err)
	}
	anon := f.browser()
	status, body := anon.do(http.MethodGet, "/api/v1/auth/capabilities", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"email_delivery":true`) {
		t.Fatalf("capabilities: %d %s", status, body)
	}

	var firstStatus int
	var firstBody []byte
	for i, login := range []string{"nobody", "sso", "sso@example.com", "locked", "disabled", "valid", "VALID@example.com"} {
		status, body := anon.do(http.MethodPost, "/api/v1/auth/password/forgot", map[string]string{"login": login})
		if i == 0 {
			firstStatus, firstBody = status, body
		}
		if status != http.StatusAccepted || status != firstStatus || !bytes.Equal(body, firstBody) {
			t.Fatalf("%s: %d %s, want %d %s", login, status, body, firstStatus, firstBody)
		}
	}
	if got := sink.mailsTo("valid@example.com", 2, 10*time.Second); len(got) != 2 {
		t.Fatalf("valid account got %d mails, want 2", len(got))
	}
	if got := sink.mailsTo("locked@example.com", 1, 10*time.Second); len(got) != 1 {
		t.Fatalf("locked account got %d mails, want 1", len(got))
	}
	// Two more requests: the third mail is the hourly cap.
	for i := 0; i < 2; i++ {
		anon.do(http.MethodPost, "/api/v1/auth/password/forgot", map[string]string{"login": "valid"})
	}
	time.Sleep(500 * time.Millisecond)
	if got := sink.mailsTo("valid@example.com", adminauth.PasswordResetMailsPerHour+1, 2*time.Second); len(got) != adminauth.PasswordResetMailsPerHour {
		t.Fatalf("valid account got %d mails, want the cap of %d", len(got), adminauth.PasswordResetMailsPerHour)
	}
	for _, rcpt := range []string{"sso@example.com", "disabled@example.com"} {
		if got := sink.mailsTo(rcpt, 1, 0); len(got) != 0 {
			t.Fatalf("%s was mailed", rcpt)
		}
	}

	// Only the newest link works; it resets the password, signs every
	// session out and clears the lock.
	locked := f.userID("locked")
	session := f.browser()
	if _, err := f.db.ExecContext(f.ctx, `UPDATE admin_users SET locked_until = NULL WHERE id = $1`, locked); err != nil {
		t.Fatal(err)
	}
	session.mustLogin("locked")
	if _, err := f.db.ExecContext(f.ctx, `UPDATE admin_users SET locked_until = NOW() + INTERVAL '10 minutes' WHERE id = $1`, locked); err != nil {
		t.Fatal(err)
	}
	mails := sink.mailsTo("locked@example.com", 1, 0)
	token := tokenLink(t, mails[0], "https://app.example.com/reset-password")
	if status, body := anon.doToken(http.MethodPost, "/api/v1/auth/password/reset", token, map[string]string{"password": "a-fresh-long-password"}); status != http.StatusNoContent {
		t.Fatalf("reset: %d %s", status, body)
	}
	if status := session.status(http.MethodGet, "/api/v1/account/sessions"); status != http.StatusUnauthorized {
		t.Fatalf("session after reset: %d, want signed out", status)
	}
	if status, body := f.browser().login("locked", "a-fresh-long-password"); status != http.StatusOK {
		t.Fatalf("sign-in after reset: %d %s", status, body)
	}
	f.waitAudit("auth.password_reset", locked.String())
}

// With email delivery, a superadmin's links are mailed and never returned,
// and the email change confirms through the new address.
func TestAccountLinksWithEmailDelivery(t *testing.T) {
	sink := startMailSink(t)
	f := newAccountFixture(t, sink)
	root := f.createSuperadminBrowser()
	status, body := root.do(http.MethodPost, "/api/v1/users", models.CreateAdminUserRequest{
		Username: "newcomer", Email: ptrTo("newcomer@example.com"), PlatformRole: "member",
	})
	created := decodeJSON[models.AdminUser](t, status, body, http.StatusCreated)
	status, body = root.do(http.MethodPost, "/api/v1/users/"+created.ID.String()+"/invite", nil)
	invite := decodeJSON[models.AccountLinkResponse](t, status, body, http.StatusOK)
	if !invite.Delivered || invite.Link != "" || strings.Contains(string(body), "accept-invite") {
		t.Fatalf("invite with SMTP: %s", body)
	}
	mails := sink.mailsTo("newcomer@example.com", 1, 5*time.Second)
	if len(mails) != 1 {
		t.Fatalf("%d invite mails", len(mails))
	}
	token := tokenLink(t, mails[0], "https://app.example.com/accept-invite")
	anon := f.browser()
	if status, body := anon.doToken(http.MethodPost, "/api/v1/auth/invite/accept", token, map[string]string{"password": "a-good-long-password"}); status != http.StatusNoContent {
		t.Fatalf("accept: %d %s", status, body)
	}

	// Own email change: confirmation to the new address, notice to the old.
	user := f.browser()
	if status, body := user.login("newcomer", "a-good-long-password"); status != http.StatusOK {
		t.Fatalf("login: %d %s", status, body)
	}
	status, body = user.do(http.MethodPost, "/api/v1/account/email", map[string]string{"current_password": "a-good-long-password", "new_email": "Moved@Example.com"})
	if status != http.StatusAccepted || !strings.Contains(string(body), `"pending_email":"moved@example.com"`) {
		t.Fatalf("email change: %d %s", status, body)
	}
	if got := sink.mailsTo("newcomer@example.com", 2, 5*time.Second); len(got) != 2 {
		t.Fatalf("old address got %d mails, want the invite and the change notice", len(got))
	}
	confirm := sink.mailsTo("moved@example.com", 1, 5*time.Second)
	if len(confirm) != 1 {
		t.Fatal("no confirmation mail")
	}
	var email string
	_ = f.db.QueryRowContext(f.ctx, `SELECT email FROM admin_users WHERE id = $1`, created.ID).Scan(&email)
	if email != "newcomer@example.com" {
		t.Fatalf("email changed before confirmation: %s", email)
	}
	confirmToken := tokenLink(t, confirm[0], "https://app.example.com/confirm-email")
	// A confirmation token is not a reset token.
	if status, _ := anon.doToken(http.MethodPost, "/api/v1/auth/password/reset", confirmToken, map[string]string{"password": "a-good-long-password"}); status != http.StatusBadRequest {
		t.Fatalf("confirmation token on the reset route: %d", status)
	}
	if status, body := anon.doToken(http.MethodPost, "/api/v1/auth/email/confirm", confirmToken, nil); status != http.StatusOK {
		t.Fatalf("confirm: %d %s", status, body)
	}
	_ = f.db.QueryRowContext(f.ctx, `SELECT email FROM admin_users WHERE id = $1`, created.ID).Scan(&email)
	if email != "moved@example.com" {
		t.Fatalf("email after confirmation: %s", email)
	}
	// The old address is told the change completed.
	if got := sink.mailsTo("newcomer@example.com", 3, 5*time.Second); len(got) != 3 || !strings.Contains(mailText(t, got[2]), "is now moved@example.com") {
		t.Fatalf("old address got %d mails, want invite, change notice and completion notice", len(got))
	}

	// Own password change keeps this session and signs out the others.
	other := f.browser()
	if status, _ := other.login("newcomer", "a-good-long-password"); status != http.StatusOK {
		t.Fatal("second login")
	}
	status, body = user.do(http.MethodPost, "/api/v1/account/password", map[string]string{"current_password": "wrong-password-here", "new_password": "the-next-long-password"})
	if status != http.StatusBadRequest || !strings.Contains(string(body), "invalid_password") {
		t.Fatalf("wrong current password: %d %s", status, body)
	}
	status, body = user.do(http.MethodPost, "/api/v1/account/password", map[string]string{"current_password": "a-good-long-password", "new_password": "the-next-long-password"})
	if status != http.StatusOK || !strings.Contains(string(body), `"revoked":1`) {
		t.Fatalf("password change: %d %s", status, body)
	}
	if status := user.status(http.MethodGet, "/api/v1/account/security"); status != http.StatusOK {
		t.Fatalf("the changing session: %d, want kept", status)
	}
	if status := other.status(http.MethodGet, "/api/v1/account/security"); status != http.StatusUnauthorized {
		t.Fatalf("the other session: %d, want signed out", status)
	}
	f.waitAudit("auth.password_change", created.ID.String())
	if got := sink.mailsTo("moved@example.com", 2, 5*time.Second); len(got) != 2 || !strings.Contains(mailText(t, got[1]), "from the account page") {
		t.Fatalf("password changed notice: %d mails to the address", len(got))
	}

	// A superadmin's reset link is mailed, not returned.
	status, body = root.do(http.MethodPost, "/api/v1/users/"+created.ID.String()+"/password-reset", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"delivered":true`) || strings.Contains(string(body), "reset-password") {
		t.Fatalf("reset link with SMTP: %d %s", status, body)
	}
}

// Every account route needs a browser session: an API key gets 403 (it has
// no account), and SSO accounts cannot change their password or email.
func TestAccountRoutesNeedASessionAndALocalAccount(t *testing.T) {
	f := newAccountFixture(t, nil)
	key := f.apiKey(f.tenantA, "write")
	for _, path := range []string{"/api/v1/account/security", "/api/v1/account/password"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "password") {
			method = http.MethodPost
		}
		req, _ := http.NewRequest(method, f.http.URL+path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("%s %s with an API key: %d, want 403", method, path, res.StatusCode)
		}
	}
	f.createUser("linked", "member", nil)
	if _, err := f.db.ExecContext(f.ctx, `UPDATE admin_users SET external_issuer = 'https://idp', external_subject = 's1' WHERE username = 'linked'`); err != nil {
		t.Fatal(err)
	}
	linked := f.browser().mustLogin("linked")
	status, body := linked.do(http.MethodGet, "/api/v1/account/security", nil)
	sec := decodeJSON[models.AccountSecurity](t, status, body, http.StatusOK)
	if sec.Local || sec.AuthMethod != "oidc" {
		t.Fatalf("linked account security: %+v", sec)
	}
	status, body = linked.do(http.MethodPost, "/api/v1/account/password", map[string]string{"current_password": identityPassword, "new_password": "a-good-long-password"})
	if status != http.StatusConflict || !strings.Contains(string(body), "not_local_account") {
		t.Fatalf("SSO password change: %d %s", status, body)
	}
}

// enrolOverHTTP turns two-factor on for a signed-in browser and returns the
// secret and recovery codes.
func enrolOverHTTP(t *testing.T, b *browser) (string, []string) {
	t.Helper()
	status, body := b.do(http.MethodPost, "/api/v1/account/two-factor/setup", map[string]string{"current_password": identityPassword})
	setup := decodeJSON[models.TOTPSetupResponse](t, status, body, http.StatusOK)
	code, _ := adminauth.TOTPCodeAt(setup.Secret, time.Now())
	status, body = b.do(http.MethodPost, "/api/v1/account/two-factor/enable", map[string]string{"code": code})
	return setup.Secret, decodeJSON[models.RecoveryCodesResponse](t, status, body, http.StatusOK).RecoveryCodes
}

// A recovery code suggests a lost device: signing in with one ends every
// other session (the new one stays), and the account is mailed.
func TestRecoveryCodeSignInEndsOtherSessions(t *testing.T) {
	sink := startMailSink(t)
	f := newAccountFixture(t, sink)
	f.createUser("alice", "member", nil)
	if _, err := f.db.ExecContext(f.ctx, `UPDATE admin_users SET email = 'alice@example.com' WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}
	laptop := f.browser().mustLogin("alice")
	_, codes := enrolOverHTTP(t, laptop)

	phone := f.browser()
	status, body := phone.login("alice", identityPassword)
	challenge := decodeJSON[models.MFAChallengeResponse](t, status, body, http.StatusOK)
	status, body = phone.do(http.MethodPost, "/api/v1/auth/login/mfa", map[string]string{"mfa_token": challenge.MFAToken, "code": codes[0]})
	if status != http.StatusOK {
		t.Fatalf("recovery code sign-in: %d %s", status, body)
	}
	if status := laptop.status(http.MethodGet, "/api/v1/account/security"); status != http.StatusUnauthorized {
		t.Fatalf("other session after a recovery code: %d, want signed out", status)
	}
	if status := phone.status(http.MethodGet, "/api/v1/account/security"); status != http.StatusOK {
		t.Fatalf("the recovery-code session: %d", status)
	}
	if got := sink.mailsTo("alice@example.com", 2, 5*time.Second); len(got) < 2 {
		t.Fatalf("%d mails, want the two-factor notice and the recovery-code notice", len(got))
	}
}

// No path turns bcrypt's 72-byte limit into a 500.
func TestOverlongPasswordsAreValidationErrors(t *testing.T) {
	f := newAccountFixture(t, nil)
	root := f.createSuperadminBrowser()
	long := strings.Repeat("密", 30) // 90 bytes, 30 characters
	status, body := root.do(http.MethodPost, "/api/v1/users", models.CreateAdminUserRequest{Username: "longpass", Password: &long, PlatformRole: "member"})
	if status != http.StatusBadRequest || !strings.Contains(string(body), "72 bytes") {
		t.Fatalf("create with 90 bytes: %d %s", status, body)
	}
	f.createUser("alice", "member", nil)
	alice := f.userID("alice")
	status, body = root.do(http.MethodPatch, "/api/v1/users/"+alice.String(), models.UpdateAdminUserRequest{Password: &long})
	if status != http.StatusBadRequest {
		t.Fatalf("superadmin set with 90 bytes: %d %s", status, body)
	}
	user := f.browser().mustLogin("alice")
	status, body = user.do(http.MethodPost, "/api/v1/account/password", map[string]string{"current_password": identityPassword, "new_password": long})
	if status != http.StatusBadRequest {
		t.Fatalf("own change with 90 bytes: %d %s", status, body)
	}
	status, body = root.do(http.MethodPost, "/api/v1/users/"+alice.String()+"/password-reset", nil)
	reset := decodeJSON[models.AccountLinkResponse](t, status, body, http.StatusOK)
	token := strings.TrimPrefix(reset.Link, "/reset-password#")
	if status, body := f.browser().doToken(http.MethodPost, "/api/v1/auth/password/reset", token, map[string]string{"password": long}); status != http.StatusBadRequest {
		t.Fatalf("reset with 90 bytes: %d %s", status, body)
	}
	// 24 three-byte characters are exactly 72 bytes: accepted.
	fits := strings.Repeat("密", 24)
	if status, body := f.browser().doToken(http.MethodPost, "/api/v1/auth/password/reset", token, map[string]string{"password": fits}); status != http.StatusNoContent {
		t.Fatalf("reset with 72 bytes: %d %s", status, body)
	}
	if status, _ := f.browser().login("alice", fits); status != http.StatusOK {
		t.Fatal("sign-in with the 72-byte password")
	}
}

// With single sign-on configured, inviting an account not linked yet needs
// an explicit method: a password invite would keep it from ever being linked.
func TestInviteNeedsAnExplicitMethodWithSSO(t *testing.T) {
	f := newAccountFixture(t, nil, func(cfg *config.APIConfig) {
		cfg.OIDC.Enabled = true
		cfg.OIDC.IssuerURL = "https://idp.invalid"
		cfg.OIDC.ClientID = "probara"
	})
	root := f.createSuperadminBrowser()
	status, body := root.do(http.MethodPost, "/api/v1/users", models.CreateAdminUserRequest{
		Username: "pending", Email: ptrTo("pending@example.com"), PlatformRole: "member",
	})
	created := decodeJSON[models.AdminUser](t, status, body, http.StatusCreated)
	path := "/api/v1/users/" + created.ID.String() + "/invite"
	if status, body := root.do(http.MethodPost, path, nil); status != http.StatusBadRequest || !strings.Contains(string(body), "choose the invite method") {
		t.Fatalf("invite without a method: %d %s", status, body)
	}
	status, body = root.do(http.MethodPost, path, models.SendInviteRequest{Method: "sso"})
	if sso := decodeJSON[models.AccountLinkResponse](t, status, body, http.StatusOK); sso.Method != "sso" || sso.Link != "/login" {
		t.Fatalf("SSO invite: %s", body)
	}
	status, body = root.do(http.MethodPost, path, models.SendInviteRequest{Method: "password"})
	if pw := decodeJSON[models.AccountLinkResponse](t, status, body, http.StatusOK); pw.Method != "password" || !strings.HasPrefix(pw.Link, "/accept-invite#") {
		t.Fatalf("explicit password invite: %s", body)
	}
}

// A superadmin's address change retires mailed links, and a superadmin's
// password set revokes the user's MCP connections and mails a notice.
func TestSuperadminEditsRetireLinksAndMCPConnections(t *testing.T) {
	sink := startMailSink(t)
	f := newAccountFixture(t, sink)
	root := f.createSuperadminBrowser()
	f.createUser("alice", "member", nil)
	alice := f.userID("alice")
	if _, err := f.db.ExecContext(f.ctx, `UPDATE admin_users SET email = 'alice@example.com' WHERE id = $1`, alice); err != nil {
		t.Fatal(err)
	}
	anon := f.browser()
	if status, _ := anon.do(http.MethodPost, "/api/v1/auth/password/forgot", map[string]string{"login": "alice"}); status != http.StatusAccepted {
		t.Fatal("forgot")
	}
	mails := sink.mailsTo("alice@example.com", 1, 5*time.Second)
	if len(mails) != 1 {
		t.Fatalf("%d reset mails", len(mails))
	}
	token := tokenLink(t, mails[0], "https://app.example.com/reset-password")

	// Saving the same address (another field edited) keeps the link.
	if status, body := root.do(http.MethodPatch, "/api/v1/users/"+alice.String(), map[string]any{"email": "Alice@Example.com", "platform_role": "member"}); status != http.StatusOK {
		t.Fatalf("edit: %d %s", status, body)
	}
	var open int
	_ = f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM admin_account_tokens WHERE admin_user_id = $1 AND revoked_at IS NULL AND used_at IS NULL`, alice).Scan(&open)
	if open != 1 {
		t.Fatalf("an unchanged address retired the link (%d open)", open)
	}
	if status, body := root.do(http.MethodPatch, "/api/v1/users/"+alice.String(), map[string]any{"email": "new@example.com"}); status != http.StatusOK {
		t.Fatalf("address change: %d %s", status, body)
	}
	if status, _ := anon.doToken(http.MethodPost, "/api/v1/auth/password/reset", token, map[string]string{"password": "a-good-long-password"}); status != http.StatusBadRequest {
		t.Fatalf("reset link after an address change: %d, want refused", status)
	}

	var client, grant uuid.UUID
	if err := f.db.QueryRowContext(f.ctx, `INSERT INTO mcp_oauth_clients (client_name, redirect_uris) VALUES ('agent', ARRAY['http://127.0.0.1/cb']) RETURNING id`).Scan(&client); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(f.ctx, `INSERT INTO mcp_oauth_grants (client_id, admin_user_id, tenant_id, scope) VALUES ($1, $2, $3, 'write') RETURNING id`, client, alice, f.tenantA).Scan(&grant); err != nil {
		t.Fatal(err)
	}
	password := "a-new-long-password"
	if status, body := root.do(http.MethodPatch, "/api/v1/users/"+alice.String(), models.UpdateAdminUserRequest{Password: &password}); status != http.StatusOK {
		t.Fatalf("password set: %d %s", status, body)
	}
	var revoked bool
	_ = f.db.QueryRowContext(f.ctx, `SELECT revoked_at IS NOT NULL FROM mcp_oauth_grants WHERE id = $1`, grant).Scan(&revoked)
	if !revoked {
		t.Fatal("a superadmin password set left the MCP grant live")
	}
	if got := sink.mailsTo("new@example.com", 1, 5*time.Second); len(got) != 1 || !strings.Contains(mailText(t, got[0]), "by an administrator") {
		t.Fatalf("password changed notice: %d mails", len(got))
	}
}
