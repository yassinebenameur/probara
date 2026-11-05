package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
)

// Two-factor sign-in (B4c). A correct password on an account with TOTP
// enabled answers {mfa_required, mfa_token} and sets no cookie; the token
// proves step one for mfaTokenTTL and POST /auth/login/mfa completes the
// sign-in with a code.
//
// The step-one token is NOT a JWT: it is "pmfa1~payload~mac" with its own
// HMAC key derived from ADMIN_JWT_SECRET, so it can never be mistaken for
// (or replayed as) an access token. It binds the account's current password
// hash and TOTP enrollment (adminauth.SecondFactorRequired.Binding), so a
// password change or 2FA reset kills outstanding tokens.

const (
	mfaTokenPrefix = "pmfa1"
	mfaTokenTTL    = 5 * time.Minute
)

type mfaTokenPayload struct {
	UserID  string `json:"u"`
	Binding string `json:"b"`
	Expires int64  `json:"e"`
	Nonce   string `json:"n"`
}

func (h *Handlers) mfaKey() []byte {
	mac := hmac.New(sha256.New, []byte(h.cfg.AdminJWTSecret))
	mac.Write([]byte("probara mfa step-one token v1"))
	return mac.Sum(nil)
}

func (h *Handlers) mintMFAToken(userID uuid.UUID, binding string, now time.Time) (string, error) {
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	raw, err := json.Marshal(mfaTokenPayload{
		UserID: userID.String(), Binding: binding,
		Expires: now.Add(mfaTokenTTL).Unix(), Nonce: base64.RawURLEncoding.EncodeToString(nonce),
	})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, h.mfaKey())
	mac.Write([]byte(mfaTokenPrefix + "~" + payload))
	return mfaTokenPrefix + "~" + payload + "~" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (h *Handlers) parseMFAToken(token string, now time.Time) (uuid.UUID, string, error) {
	parts := strings.Split(token, "~")
	if len(parts) != 3 || parts[0] != mfaTokenPrefix {
		return uuid.Nil, "", fmt.Errorf("malformed token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("malformed token")
	}
	mac := hmac.New(sha256.New, h.mfaKey())
	mac.Write([]byte(parts[0] + "~" + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return uuid.Nil, "", fmt.Errorf("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("malformed token")
	}
	var p mfaTokenPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return uuid.Nil, "", fmt.Errorf("malformed token")
	}
	if now.Unix() >= p.Expires {
		return uuid.Nil, "", fmt.Errorf("expired")
	}
	id, err := uuid.Parse(p.UserID)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("malformed token")
	}
	return id, p.Binding, nil
}

// answerSecondFactor is Login's reply for a correct password on a 2FA
// account: a step-one token, no session.
func (h *Handlers) answerSecondFactor(w http.ResponseWriter, sf *adminauth.SecondFactorRequired) {
	token, err := h.mintMFAToken(sf.UserID, sf.Binding, time.Now())
	if err != nil {
		h.logger.WithError(err).Error("Failed to mint second-factor token")
		errors.WriteInternalError(w, "failed to sign in")
		return
	}
	writeJSON(w, http.StatusOK, models.MFAChallengeResponse{
		MFARequired: true, MFAToken: token, ExpiresIn: int(mfaTokenTTL.Seconds()),
	})
}

// LoginMFA handles POST /api/v1/auth/login/mfa (public): step two of a
// password sign-in. Code is a TOTP code or a recovery code; wrong codes
// count toward the same lockout as wrong passwords.
func (h *Handlers) LoginMFA(w http.ResponseWriter, r *http.Request) {
	var req models.MFALoginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	userID, binding, err := h.parseMFAToken(strings.TrimSpace(req.MFAToken), time.Now())
	if err != nil {
		writeMFATokenInvalid(w)
		return
	}
	method, err := h.service.CompleteSecondFactor(r.Context(), userID, binding, req.Code)
	if err != nil {
		var failure *adminauth.LoginFailure
		switch {
		case stderrors.Is(err, adminauth.ErrTokenInvalid):
			writeMFATokenInvalid(w)
		case stderrors.As(err, &failure):
			label := ""
			if user, uerr := h.service.GetAdminByID(r.Context(), userID); uerr == nil {
				label = user.Username
			}
			if failure.Locked {
				h.recordLoginRefusal(r, label, &userID, "locked")
				writeLoginThrottled(w, failure.RetryAfter)
				return
			}
			h.recordUserEvent(r, "auth.login", audit.OutcomeFailure, userID, label, map[string]any{"factor": "second"})
			if failure.LockedNow {
				h.recordLockout(r, label, userID, failure)
			}
			errors.WriteUnauthorizedError(w, "invalid authentication code")
		default:
			h.logger.WithError(err).Error("Second-factor sign-in failed")
			errors.WriteInternalError(w, "failed to sign in")
		}
		return
	}

	user, err := h.service.GetAdminByID(r.Context(), userID)
	if err != nil {
		h.logger.WithError(err).Error("Failed to load admin after second factor")
		errors.WriteInternalError(w, "failed to sign in")
		return
	}
	h.recordUserEvent(r, "auth.login", audit.OutcomeSuccess, user.ID, user.Username, map[string]any{"mfa": method})
	if err := h.service.UpdateLastLogin(r.Context(), user.ID); err != nil {
		h.logger.WithError(err).Warn("Failed to update admin last login")
	}
	if err := h.issueSession(w, r, user.ID, adminauth.AuthMethodPassword); err != nil {
		h.logger.WithError(err).Error("Failed to issue admin session")
		errors.WriteInternalError(w, "failed to create session")
		return
	}
	writeJSON(w, http.StatusOK, models.AdminAuthResponse{User: *user})
}

func writeMFATokenInvalid(w http.ResponseWriter) {
	errors.WriteError(w, http.StatusUnauthorized, "mfa_token_invalid", "the sign-in attempt expired; enter your password again")
}

// GetMyMFA handles GET /api/v1/account/2fa.
func (h *Handlers) GetMyMFA(w http.ResponseWriter, r *http.Request) {
	adminID, _, ok := sessionUser(w, r)
	if !ok {
		return
	}
	account, err := h.service.LoadAccount(r.Context(), adminID)
	if err != nil {
		h.writeLifecycleError(w, err, "load account")
		return
	}
	st, err := h.service.GetMFAStatus(r.Context(), adminID)
	if err != nil {
		h.writeLifecycleError(w, err, "load two-factor status")
		return
	}
	writeJSON(w, http.StatusOK, models.AccountMFAStatus{
		Available: st.Available, Eligible: account.Local(), Enabled: st.Enabled,
		EnabledAt: st.EnabledAt, RecoveryCodesRemaining: st.RecoveryCodesRemaining,
	})
}

// SetupMyTOTP handles POST /api/v1/account/2fa/setup: re-checks the
// password and starts a pending enrollment (not active until confirmed).
func (h *Handlers) SetupMyTOTP(w http.ResponseWriter, r *http.Request) {
	adminID, _, ok := sessionUser(w, r)
	if !ok {
		return
	}
	var req models.PasswordConfirmRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	account, err := h.service.LoadAccount(r.Context(), adminID)
	if err != nil {
		h.writeLifecycleError(w, err, "load account")
		return
	}
	if !account.Local() {
		h.writeLifecycleError(w, adminauth.ErrNotLocalAccount, "")
		return
	}
	if err := h.service.VerifyPassword(r.Context(), adminID, req.CurrentPassword); err != nil {
		h.writeCredentialFailure(w, r, adminID, err)
		return
	}
	secret, err := h.service.BeginTOTPEnrollment(r.Context(), adminID)
	if err != nil {
		h.writeMFAError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models.TOTPSetupResponse{Secret: secret, OTPAuthURI: adminauth.TOTPURI(account.Username, secret)})
}

// EnableMyTOTP handles POST /api/v1/account/2fa/enable: confirms the
// pending enrollment with a code and returns the recovery codes, once.
func (h *Handlers) EnableMyTOTP(w http.ResponseWriter, r *http.Request) {
	adminID, _, ok := sessionUser(w, r)
	if !ok {
		return
	}
	var req models.TOTPCodeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	codes, err := h.service.EnableTOTP(r.Context(), adminID, req.Code)
	if err != nil {
		h.writeMFAError(w, err)
		return
	}
	h.recordSessionEvent(r, "auth.mfa_enable", adminID, map[string]any{"method": adminauth.MFAMethodTOTP})
	writeJSON(w, http.StatusOK, models.RecoveryCodesResponse{RecoveryCodes: codes})
}

// DisableMyTOTP handles POST /api/v1/account/2fa/disable: needs the
// password and a current code (or a recovery code).
func (h *Handlers) DisableMyTOTP(w http.ResponseWriter, r *http.Request) {
	adminID, _, ok := sessionUser(w, r)
	if !ok {
		return
	}
	var req models.TOTPProofRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !h.requireEnrolled(w, r, adminID) {
		return
	}
	if _, err := h.service.VerifyCredentials(r.Context(), adminID, req.CurrentPassword, req.Code, true); err != nil {
		h.writeCredentialFailure(w, r, adminID, err)
		return
	}
	if _, err := h.service.DisableTOTP(r.Context(), adminID); err != nil {
		h.writeMFAError(w, err)
		return
	}
	h.recordSessionEvent(r, "auth.mfa_disable", adminID, nil)
	w.WriteHeader(http.StatusNoContent)
}

// RegenerateMyRecoveryCodes handles POST /api/v1/account/2fa/recovery-codes:
// replaces every recovery code (password and a current code required).
func (h *Handlers) RegenerateMyRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	adminID, _, ok := sessionUser(w, r)
	if !ok {
		return
	}
	var req models.TOTPProofRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !h.requireEnrolled(w, r, adminID) {
		return
	}
	if _, err := h.service.VerifyCredentials(r.Context(), adminID, req.CurrentPassword, req.Code, true); err != nil {
		h.writeCredentialFailure(w, r, adminID, err)
		return
	}
	codes, err := h.service.RegenerateRecoveryCodes(r.Context(), adminID)
	if err != nil {
		h.writeMFAError(w, err)
		return
	}
	h.recordSessionEvent(r, "auth.mfa_recovery_codes", adminID, nil)
	writeJSON(w, http.StatusOK, models.RecoveryCodesResponse{RecoveryCodes: codes})
}

func (h *Handlers) requireEnrolled(w http.ResponseWriter, r *http.Request, adminID uuid.UUID) bool {
	st, err := h.service.GetMFAStatus(r.Context(), adminID)
	if err != nil {
		h.writeLifecycleError(w, err, "load two-factor status")
		return false
	}
	if !st.Enabled {
		h.writeMFAError(w, adminauth.ErrMFANotEnabled)
		return false
	}
	return true
}

func (h *Handlers) writeMFAError(w http.ResponseWriter, err error) {
	switch {
	case stderrors.Is(err, adminauth.ErrInvalidMFACode):
		errors.WriteError(w, http.StatusBadRequest, "invalid_code", "the authentication code is incorrect")
	case stderrors.Is(err, adminauth.ErrMFAAlreadyEnabled), stderrors.Is(err, adminauth.ErrMFANotEnabled), stderrors.Is(err, adminauth.ErrMFANoEnrollment):
		errors.WriteError(w, http.StatusConflict, "conflict", err.Error())
	default:
		h.writeLifecycleError(w, err, "update two-factor authentication")
	}
}

// ResetUserMFA handles POST /api/v1/users/{id}/reset-2fa (superadmin):
// turns a user's two-factor off (lost device), so they sign in with the
// password alone and can enroll again.
func (h *Handlers) ResetUserMFA(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		errors.WriteValidationError(w, "invalid user ID")
		return
	}
	was, err := h.service.ResetMFA(r.Context(), userID)
	if err != nil {
		h.writeLifecycleError(w, err, "reset two-factor authentication")
		return
	}
	h.recordSessionEvent(r, "auth.mfa_reset", userID, map[string]any{"was_enabled": was})
	writeJSON(w, http.StatusOK, models.ResetMFAResponse{WasEnabled: was})
}
