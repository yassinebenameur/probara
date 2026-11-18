package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
)

// LoginMFA handles POST /api/v1/auth/login/mfa, the second step of a
// two-factor sign-in: the challenge Login answered a correct password with,
// and a code from the authenticator app or a recovery code. Only success
// sets the session cookies.
//
// A wrong code, a locked account and an account that was disabled meanwhile
// all get the same 401 and body, as in the first step, and each costs a
// sign-in attempt toward the lockout. An unusable challenge (unknown, used,
// expired, or retired by a password or two-factor reset) is its own 401: the
// sign-in starts again, and it tells nothing about the account. The rate
// limiter treats this route as a sign-in (refused while the address's
// failure budget is empty, 401s charged to it).
func (h *Handlers) LoginMFA(w http.ResponseWriter, r *http.Request) {
	var req models.LoginMFARequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body")
		return
	}
	challenge := strings.TrimSpace(req.MFAToken)
	if challenge == "" || strings.TrimSpace(req.Code) == "" {
		apierrors.WriteValidationError(w, "mfa_token and code are required")
		return
	}

	done, err := h.service.CompleteSecondFactor(r.Context(), challenge, req.Code)
	if err != nil {
		var failure *adminauth.LoginFailure
		switch {
		case errors.Is(err, adminauth.ErrMFAChallengeInvalid):
			apierrors.WriteError(w, http.StatusUnauthorized, "mfa_challenge_invalid", err.Error())
		case errors.As(err, &failure):
			h.recordSecondStepFailure(r, failure)
			if failure.SecondFactorLockouts > 0 {
				h.notifySecondFactorLockout(r, *failure.UserID, failure.Held)
			}
			apierrors.WriteUnauthorizedError(w, "invalid authentication code")
		case errors.Is(err, adminauth.ErrSecretUnreadable):
			// A configuration fault, not the user's: the attempt was given
			// back. Said loudly once per process; the answer stays generic
			// (an error tied to one account must not set it apart).
			secretUnreadableOnce.Do(func() {
				h.logger.WithError(err).Error("Two-factor sign-in cannot check authenticator codes; set the PROBARA_SECRETS_KEY versions that sealed them, or reset two-factor for the affected users (recovery codes still work)")
			})
			apierrors.WriteUnauthorizedError(w, "invalid authentication code")
		default:
			h.logger.WithError(err).Error("Failed to check second sign-in factor")
			apierrors.WriteUnauthorizedError(w, "invalid authentication code")
		}
		return
	}

	user := done.User
	h.recordAuthEventDetails(r, "auth.login", audit.OutcomeSuccess, user.Username, &user.ID,
		map[string]any{"second_factor": done.Method})
	if err := h.service.UpdateLastLogin(r.Context(), user.ID); err != nil {
		h.logger.WithError(err).Warn("Failed to update admin last login")
	}
	familyID, err := h.issueSession(w, r, user.ID, adminauth.AuthMethodPassword, done.Method)
	if err != nil {
		h.logger.WithError(err).Error("Failed to issue admin session")
		apierrors.WriteInternalError(w, "failed to create session")
		return
	}
	if done.Method == adminauth.SecondFactorRecoveryCode {
		// A recovery code suggests a lost device: every other session ends.
		revoked, err := h.service.RevokeAllSessions(r.Context(), user.ID, familyID, adminauth.RevokeReasonRecoveryCode)
		if err != nil {
			h.logger.WithError(err).Error("Failed to sign out other sessions after a recovery code")
		}
		h.recordSessionEvent(r, "auth.sessions_revoke_others", audit.OutcomeSuccess, user.ID, familyID,
			map[string]any{"revoked": revoked, "reason": adminauth.RevokeReasonRecoveryCode})
		h.notifyRecoveryCodeUsed(r, user.ID, done.RecoveryCodesRemaining)
	}

	full, err := h.service.GetAdminByID(r.Context(), user.ID)
	if err != nil {
		h.logger.WithError(err).Warn("Failed to load admin user")
		full = &user
	}
	httpx.WriteJSON(w, http.StatusOK, models.AdminAuthResponse{User: *full})
}

// recordSecondStepFailure audits a refused second step like a refused
// password (auth.login failure, auth.lockout when it locked the account),
// marked as the second factor.
func (h *Handlers) recordSecondStepFailure(r *http.Request, failure *adminauth.LoginFailure) {
	if h.audit == nil || failure.UserID == nil {
		return
	}
	label := ""
	if user, err := h.service.GetAdminByID(r.Context(), *failure.UserID); err == nil {
		label = user.Username
	}
	details := map[string]any{"step": "second_factor"}
	if failure.Locked {
		details["reason"] = "locked"
	}
	h.recordAuthEventDetails(r, "auth.login", audit.OutcomeFailure, label, failure.UserID, details)
	if failure.LockedNow {
		lock := audit.FromRequest(r)
		lock.Action = "auth.lockout"
		if failure.SecondFactorLockouts > 0 {
			// Wrong codes locked it: whoever signs in has the password.
			lock.Action = "auth.second_factor_lockout"
			lock.MergeDetails(map[string]any{
				"lockouts_24h": failure.SecondFactorLockouts,
				"held":         failure.Held,
			})
		}
		lock.Outcome = audit.OutcomeSuccess
		lock.ActorLabel = label
		lock.ActorType = audit.ActorAdminUser
		lock.ActorID = failure.UserID
		lock.ResourceType = "user"
		lock.ResourceID = failure.UserID.String()
		lock.MergeDetails(map[string]any{
			"failed_attempts": adminauth.LockoutThreshold,
			"locked_until":    failure.LockedUntil.UTC(),
			"step":            "second_factor",
		})
		h.audit.Record(lock)
	}
}

// recordAuthEventDetails is recordAuthEvent with details.
func (h *Handlers) recordAuthEventDetails(r *http.Request, action, outcome, label string, actorID *uuid.UUID, details map[string]any) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.Action = action
	event.Outcome = outcome
	event.ActorLabel = label
	if actorID != nil {
		event.ActorType = audit.ActorAdminUser
		event.ActorID = actorID
	}
	event.MergeDetails(details)
	h.audit.Record(event)
}

// secretUnreadableOnce keeps an unreadable-secret fault to one error line per
// process, however many sign-ins hit it.
var secretUnreadableOnce sync.Once

// notifySecondFactorLockout mails the account that wrong codes locked it,
// when account mail is configured.
func (h *Handlers) notifySecondFactorLockout(r *http.Request, userID uuid.UUID, held bool) {
	if !h.mail.Enabled() {
		return
	}
	account, err := h.service.LoadAccount(r.Context(), userID)
	if err != nil || account.Email == nil || *account.Email == "" {
		return
	}
	to, username := *account.Email, account.Username
	h.mail.Background(h.logger, "second-factor lockout notice", func(ctx context.Context) error {
		return h.mail.SecondFactorLockout(ctx, to, username, held)
	})
}

// notifyRecoveryCodeUsed mails the account that a recovery code signed it
// in, when account mail is configured.
func (h *Handlers) notifyRecoveryCodeUsed(r *http.Request, userID uuid.UUID, remaining int) {
	if !h.mail.Enabled() {
		return
	}
	account, err := h.service.LoadAccount(r.Context(), userID)
	if err != nil || account.Email == nil || *account.Email == "" {
		return
	}
	to, username := *account.Email, account.Username
	h.mail.Background(h.logger, "recovery code notice", func(ctx context.Context) error {
		return h.mail.RecoveryCodeUsed(ctx, to, username, remaining)
	})
}
