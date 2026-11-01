package api

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/secret"
	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// RotMG names are 1 to 10 letters.
var ignPattern = regexp.MustCompile(`^[A-Za-z]{1,10}$`)

// A session is extended at most this often, so not every request writes to the database.
const sessionTouchInterval = time.Hour

// dummyHash is checked against when an IGN does not exist, so a failed login takes as long either way.
var dummyHash = sync.OnceValue(func() string { return secret.HashPassword("not a real password") })

type userResponse struct {
	User *store.User `json:"user"`
}

func validIGN(raw string) (string, error) {
	ign := strings.TrimSpace(raw)
	if !ignPattern.MatchString(ign) {
		return "", errBadRequest("IGN must be 1 to 10 letters")
	}
	return ign, nil
}

func validNewPassword(pw string) error {
	if n := utf8.RuneCountInString(pw); n < 8 || n > 128 {
		return errBadRequest("Password must be 8 to 128 characters")
	}
	return nil
}

// principal returns the logged-in user and the hash of their session token.
func (s *Server) principal(r *http.Request) (*store.User, string, error) {
	header := r.Header.Get("Authorization")
	if len(header) < 7 || !strings.EqualFold(header[:7], "Bearer ") {
		return nil, "", errUnauthorized("missing_token", "Log in first")
	}
	hash := secret.HashToken(strings.TrimSpace(header[7:]))
	ctx := r.Context()
	now := s.nowMs()

	sess, err := store.FindSession(ctx, s.store.DB(), hash)
	if err != nil {
		return nil, "", err
	}
	if sess == nil || sess.ExpiresAt <= now {
		return nil, "", errUnauthorized("invalid_session", "Session expired, log in again")
	}
	user, err := store.GetUser(ctx, s.store.DB(), sess.UserID)
	if err != nil {
		return nil, "", err
	}
	if user == nil || !user.Active() {
		return nil, "", errUnauthorized("invalid_session", "Session expired, log in again")
	}
	if now-sess.LastUsedAt > sessionTouchInterval.Milliseconds() {
		err := s.store.Tx(ctx, func(q store.Querier) error {
			return store.TouchSession(ctx, q, hash, now, now+s.cfg.SessionTTL.Milliseconds())
		})
		if err != nil {
			return nil, "", err
		}
	}
	return user, hash, nil
}

// require returns the logged-in user if they have at least role min.
func (s *Server) require(r *http.Request, min store.Role) (*store.User, error) {
	user, _, err := s.principal(r)
	if err != nil {
		return nil, err
	}
	if user.Role.Rank() < min.Rank() {
		return nil, errForbidden("insufficient_role", "You need to be "+string(min)+" for this")
	}
	return user, nil
}

// findRoleToken looks up a usable token from user input. nil if the input is malformed, unknown, used or expired.
func findRoleToken(ctx context.Context, q store.Querier, input string, now int64) (*store.RoleToken, error) {
	canonical, ok := secret.NormalizeRoleToken(input)
	if !ok {
		return nil, nil
	}
	return store.FindUsableRoleToken(ctx, q, secret.HashToken(canonical), now)
}

// grantRole redeems token for user (inside a transaction) and returns the updated user. An admin token makes the
// user root admin if there is none yet. A pending account is approved by it.
func grantRole(ctx context.Context, q store.Querier, token *store.RoleToken, user *store.User, now int64) (*store.User, error) {
	root := user.Root
	if token.Kind == store.RoleAdmin && !root {
		exists, err := store.RootExists(ctx, q)
		if err != nil {
			return nil, err
		}
		root = !exists
	}
	if user.Role.Rank() >= token.Kind.Rank() && root == user.Root {
		return nil, errConflict("role_already_held", "Your account already has this role")
	}
	role := user.Role
	if token.Kind.Rank() > role.Rank() {
		role = token.Kind
	}
	if err := store.MarkRoleTokenUsed(ctx, q, token.ID, user.ID, now); err != nil {
		return nil, err
	}
	if err := store.SetRole(ctx, q, user.ID, role, root); err != nil {
		return nil, err
	}
	if !user.Active() {
		if err := store.ApproveUser(ctx, q, user.ID, token.CreatedBy, now); err != nil {
			return nil, err
		}
	}
	return store.GetUser(ctx, q, user.ID)
}

// badToken counts a wrong role token against the IP (and account, if logged in) and returns the error to send.
func (s *Server) badToken(ip string, user *store.User) error {
	s.limits.tokenIP.Record(ip)
	if user != nil {
		s.limits.tokenUser.Record(userKey(user.ID))
		s.log.Warn("wrong role token", "ip", ip, "ign", user.IGN)
	} else {
		s.log.Warn("wrong role token at registration", "ip", ip)
	}
	return &apiError{Status: http.StatusBadRequest, Code: "invalid_token", Message: "This token is invalid, expired or already used"}
}

func userKey(id int64) string { return "user:" + strconv.FormatInt(id, 10) }

type credentials struct {
	IGN      string `json:"ign"`
	Password string `json:"password"`
	Token    string `json:"token"`  // register only, optional: an admin or moderator token approves the account
	Client   string `json:"client"` // login only, optional: e.g. "Loot Gobbler v0.5.0", shown in session lists later
}

// register creates an account. Without a token it waits for approval (202); with a valid token it is active
// right away with that token's role (201).
func (s *Server) register(w http.ResponseWriter, r *http.Request) error {
	ip := s.clientIP(r)
	var req credentials
	if err := decodeJSON(w, r, smallBody, &req, false); err != nil {
		return err
	}
	ign, err := validIGN(req.IGN)
	if err != nil {
		return err
	}
	if err := validNewPassword(req.Password); err != nil {
		return err
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		if err := checkLimit(s.limits.register, ip); err != nil {
			return err
		}
		s.limits.register.Record(ip)
	} else if err := checkLimit(s.limits.tokenIP, ip); err != nil {
		return err
	}

	hash := secret.HashPassword(req.Password)
	ctx := r.Context()
	now := s.nowMs()
	var user *store.User
	wrongToken := false
	err = s.store.Tx(ctx, func(q store.Querier) error {
		existing, _, err := store.FindUserByIGN(ctx, q, ign)
		if err != nil {
			return err
		}
		if existing != nil {
			return errConflict("ign_taken", "This IGN is already registered or waiting for approval")
		}
		var grant *store.RoleToken
		if token != "" {
			if grant, err = findRoleToken(ctx, q, token, now); err != nil {
				return err
			}
			if grant == nil {
				wrongToken = true
				return nil
			}
		}
		id, err := store.CreatePendingUser(ctx, q, ign, hash, now)
		if err != nil {
			return err
		}
		pending := &store.User{ID: id, IGN: ign, Status: store.StatusPending, Role: store.RoleUser}
		if grant != nil {
			user, err = grantRole(ctx, q, grant, pending, now)
		} else {
			user, err = store.GetUser(ctx, q, id)
		}
		return err
	})
	if err != nil {
		return err
	}
	if wrongToken {
		return s.badToken(ip, nil)
	}
	if user.Active() {
		s.log.Info("account created with token", "ign", user.IGN, "role", user.Role, "root", user.Root, "ip", ip)
		writeJSON(w, http.StatusCreated, userResponse{user})
	} else {
		s.log.Info("registration request, waiting for approval", "ign", user.IGN, "ip", ip)
		writeJSON(w, http.StatusAccepted, userResponse{user})
	}
	return nil
}

type loginResponse struct {
	Token     string      `json:"token"`
	ExpiresAt int64       `json:"expiresAt"`
	User      *store.User `json:"user"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	ip := s.clientIP(r)
	var req credentials
	if err := decodeJSON(w, r, smallBody, &req, false); err != nil {
		return err
	}
	ign := strings.TrimSpace(req.IGN)
	accountKey := ip + "|" + strings.ToLower(ign)
	if err := checkLimit(s.limits.loginIP, ip); err != nil {
		return err
	}
	if err := checkLimit(s.limits.loginAccount, accountKey); err != nil {
		return err
	}

	ctx := r.Context()
	var user *store.User
	hash := dummyHash()
	if ignPattern.MatchString(ign) {
		u, h, err := store.FindUserByIGN(ctx, s.store.DB(), ign)
		if err != nil {
			return err
		}
		if u != nil {
			user, hash = u, h
		}
	}
	if !secret.VerifyPassword(req.Password, hash) || user == nil {
		s.limits.loginIP.Record(ip)
		s.limits.loginAccount.Record(accountKey)
		if ignPattern.MatchString(ign) {
			s.log.Warn("failed login", "ign", ign, "ip", ip)
		} else {
			s.log.Warn("failed login", "ip", ip)
		}
		return errUnauthorized("invalid_credentials", "Wrong IGN or password")
	}
	s.limits.loginAccount.Reset(accountKey)
	if !user.Active() {
		return errForbidden("pending_approval", "Your account is waiting for approval by a moderator or admin")
	}

	token := secret.NewSessionToken()
	now := s.nowMs()
	expires := now + s.cfg.SessionTTL.Milliseconds()
	client := req.Client
	if len(client) > 64 {
		client = client[:64]
	}
	err := s.store.Tx(ctx, func(q store.Querier) error {
		if err := store.TouchLogin(ctx, q, user.ID, now); err != nil {
			return err
		}
		return store.CreateSession(ctx, q, secret.HashToken(token), user.ID, now, expires, client)
	})
	if err != nil {
		return err
	}
	user.LastLoginAt = &now
	writeJSON(w, http.StatusOK, loginResponse{Token: token, ExpiresAt: expires, User: user})
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	_, hash, err := s.principal(r)
	if err != nil {
		return err
	}
	if err := store.DeleteSession(r.Context(), s.store.DB(), hash); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.principal(r)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, userResponse{user})
	return nil
}

// checkPassword verifies the user's current password, rate limited per account.
func (s *Server) checkPassword(r *http.Request, user *store.User, password string) error {
	key := userKey(user.ID)
	if err := checkLimit(s.limits.password, key); err != nil {
		return err
	}
	hash, err := store.PasswordHash(r.Context(), s.store.DB(), user.ID)
	if err != nil {
		return err
	}
	if !secret.VerifyPassword(password, hash) {
		s.limits.password.Record(key)
		// 403, not 401: the session itself is fine, and clients treat 401 as "log in again".
		return errForbidden("wrong_password", "Current password is wrong")
	}
	return nil
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) error {
	user, sessionHash, err := s.principal(r)
	if err != nil {
		return err
	}
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := decodeJSON(w, r, smallBody, &req, false); err != nil {
		return err
	}
	if err := validNewPassword(req.NewPassword); err != nil {
		return err
	}
	if err := s.checkPassword(r, user, req.CurrentPassword); err != nil {
		return err
	}
	hash := secret.HashPassword(req.NewPassword)
	ctx := r.Context()
	err = s.store.Tx(ctx, func(q store.Querier) error {
		if err := store.SetPassword(ctx, q, user.ID, hash); err != nil {
			return err
		}
		return store.DeleteUserSessions(ctx, q, user.ID, sessionHash) // other devices must log in again
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.principal(r)
	if err != nil {
		return err
	}
	if user.Root {
		return errForbidden("root_protected", "The root admin account cannot be deleted")
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, smallBody, &req, false); err != nil {
		return err
	}
	if err := s.checkPassword(r, user, req.Password); err != nil {
		return err
	}
	if err := store.DeleteUser(r.Context(), s.store.DB(), user.ID); err != nil {
		return err
	}
	s.log.Info("account deleted by its owner", "ign", user.IGN)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// redeem applies an admin or moderator token to the logged-in account.
func (s *Server) redeem(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.principal(r)
	if err != nil {
		return err
	}
	ip := s.clientIP(r)
	if err := checkLimit(s.limits.tokenIP, ip); err != nil {
		return err
	}
	if err := checkLimit(s.limits.tokenUser, userKey(user.ID)); err != nil {
		return err
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, smallBody, &req, false); err != nil {
		return err
	}
	ctx := r.Context()
	now := s.nowMs()
	var updated *store.User
	err = s.store.Tx(ctx, func(q store.Querier) error {
		grant, err := findRoleToken(ctx, q, req.Token, now)
		if err != nil || grant == nil {
			return err
		}
		current, err := store.GetUser(ctx, q, user.ID)
		if err != nil {
			return err
		}
		updated, err = grantRole(ctx, q, grant, current, now)
		return err
	})
	if err != nil {
		return err
	}
	if updated == nil {
		return s.badToken(ip, user)
	}
	s.log.Info("role token redeemed", "ign", updated.IGN, "role", updated.Role, "root", updated.Root, "ip", ip)
	writeJSON(w, http.StatusOK, userResponse{updated})
	return nil
}
