package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/secret"
	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// Forgotten passwords. Nobody but the player ever learns or sets their password:
//  1. a moderator or admin makes a one-time reset code for the player (only for players ranked below them, like the
//     other user actions); the host can make one for anyone in the console (lootgobbler reset-password <IGN>),
//     which is the way back in for the root admin
//  2. they pass it on to the player outside the app (in game, Discord...)
//  3. the player enters IGN, code and a new password on the login screen; every session of theirs ends
//
// A code works once, for that IGN only, for ResetCodeValidity. A new code replaces the player's unused one.

// ResetCodeValidity is how long a password reset code works.
const ResetCodeValidity = 24 * time.Hour

type issuedResetCode struct {
	Code      string `json:"code"` // shown once; only its hash is stored
	IGN       string `json:"ign"`
	ExpiresAt int64  `json:"expiresAt"`
}

func newResetCode(ctx context.Context, q store.Querier, userID int64, createdBy *int64, now int64) (string, int64, error) {
	code := secret.NewResetCode()
	canonical, _ := secret.NormalizeResetCode(code)
	expires := now + ResetCodeValidity.Milliseconds()
	err := store.InsertPasswordReset(ctx, q, userID, secret.HashToken(canonical), createdBy, now, expires)
	return code, expires, err
}

// createPasswordReset: POST /admin/users/{id}/password-reset (moderator or admin, for players ranked below them).
func (s *Server) createPasswordReset(w http.ResponseWriter, r *http.Request) error {
	actor, err := s.require(r, store.RoleModerator)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out issuedResetCode
	err = s.store.Tx(ctx, func(q store.Querier) error {
		target, err := targetUser(ctx, q, r, store.StatusActive)
		if err != nil {
			return err
		}
		if !outranks(actor, target) {
			return errForbidden("insufficient_rank", "You cannot reset this user's password")
		}
		code, expires, err := newResetCode(ctx, q, target.ID, &actor.ID, s.nowMs())
		out = issuedResetCode{Code: code, IGN: target.IGN, ExpiresAt: expires}
		return err
	})
	if err != nil {
		return err
	}
	s.log.Info("password reset code made", "ign", out.IGN, "by", actor.IGN)
	writeJSON(w, http.StatusCreated, out)
	return nil
}

// resetPassword: POST /auth/reset-password (public). Sets a new password with a reset code and ends every session.
func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) error {
	ip := s.clientIP(r)
	if err := checkLimit(s.limits.resetIP, ip); err != nil {
		return err
	}
	var req struct {
		IGN         string `json:"ign"`
		Code        string `json:"code"`
		NewPassword string `json:"newPassword"`
	}
	if err := decodeJSON(w, r, smallBody, &req, false); err != nil {
		return err
	}
	if err := validNewPassword(req.NewPassword); err != nil {
		return err
	}
	ign := strings.TrimSpace(req.IGN)
	canonical, codeOK := secret.NormalizeResetCode(req.Code)
	hash := secret.HashPassword(req.NewPassword) // before the transaction: Argon2 takes a moment
	ctx := r.Context()
	now := s.nowMs()
	var user *store.User
	err := s.store.Tx(ctx, func(q store.Querier) error {
		if !codeOK || !ignPattern.MatchString(ign) {
			return nil
		}
		u, _, err := store.FindUserByIGN(ctx, q, ign)
		if err != nil || u == nil || !u.Active() {
			return err
		}
		id, err := store.FindUsablePasswordReset(ctx, q, u.ID, secret.HashToken(canonical), now)
		if err != nil || id == 0 {
			return err
		}
		if err := store.MarkPasswordResetUsed(ctx, q, id, now); err != nil {
			return err
		}
		if err := store.SetPassword(ctx, q, u.ID, hash); err != nil {
			return err
		}
		user = u
		return store.DeleteUserSessions(ctx, q, u.ID, "") // whoever was logged in with the old password is out
	})
	if err != nil {
		return err
	}
	if user == nil {
		// one answer for a wrong IGN, a wrong code and a used or expired one, so it tells nothing
		s.limits.resetIP.Record(ip)
		s.log.Warn("wrong password reset code", "ip", ip)
		return &apiError{Status: http.StatusBadRequest, Code: "invalid_code",
			Message: "This reset code is wrong, expired, already used or for another IGN"}
	}
	s.limits.loginAccount.Reset(ip + "|" + strings.ToLower(ign)) // a lockout from the forgotten password ends
	s.log.Info("password reset with a code", "ign", user.IGN, "ip", ip)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// IssueConsoleResetCode makes a reset code for ign in the server console: for anyone, the root admin included.
// The user is nil (and the code empty) if there is no active account with that IGN.
func IssueConsoleResetCode(ctx context.Context, st *store.Store, ign string, now time.Time) (string, *store.User, error) {
	var code string
	var user *store.User
	err := st.Tx(ctx, func(q store.Querier) error {
		u, _, err := store.FindUserByIGN(ctx, q, strings.TrimSpace(ign))
		if err != nil || u == nil || !u.Active() {
			return err
		}
		user = u
		code, _, err = newResetCode(ctx, q, u.ID, nil, now.UnixMilli())
		return err
	})
	return code, user, err
}
