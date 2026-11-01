package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/secret"
	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// Who may do what:
//   - moderators: accept/deny registrations, remove and sign out plain users
//   - admins: also moderator tokens, removing/demoting moderators
//   - root admin (the first admin, from the console token): also promoting/demoting/removing admins
// Nobody can remove or demote the root admin.

const (
	defaultTokenHours = 24
	maxTokenHours     = 7 * 24
)

type usersResponse struct {
	Users []store.User `json:"users"`
}

// outranks reports whether actor may manage target.
func outranks(actor, target *store.User) bool {
	if target.Root || actor.ID == target.ID {
		return false
	}
	return actor.Root || actor.Role.Rank() > target.Role.Rank()
}

// targetUser loads the user named by the {id} path value, which must have the given status.
func targetUser(ctx context.Context, q store.Querier, r *http.Request, status string) (*store.User, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	u, err := store.GetUser(ctx, q, id)
	if err != nil {
		return nil, err
	}
	if u == nil || u.Status != status {
		return nil, errNotFound("No such user")
	}
	return u, nil
}

func (s *Server) listRequests(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.require(r, store.RoleModerator); err != nil {
		return err
	}
	users, err := store.ListUsers(r.Context(), s.store.DB(), store.StatusPending)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, usersResponse{users})
	return nil
}

func (s *Server) acceptRequest(w http.ResponseWriter, r *http.Request) error {
	actor, err := s.require(r, store.RoleModerator)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var user *store.User
	err = s.store.Tx(ctx, func(q store.Querier) error {
		target, err := targetUser(ctx, q, r, store.StatusPending)
		if err != nil {
			return err
		}
		if err := store.ApproveUser(ctx, q, target.ID, &actor.ID, s.nowMs()); err != nil {
			return err
		}
		user, err = store.GetUser(ctx, q, target.ID)
		return err
	})
	if err != nil {
		return err
	}
	s.log.Info("registration accepted", "ign", user.IGN, "by", actor.IGN)
	writeJSON(w, http.StatusOK, userResponse{user})
	return nil
}

func (s *Server) denyRequest(w http.ResponseWriter, r *http.Request) error {
	actor, err := s.require(r, store.RoleModerator)
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = s.store.Tx(ctx, func(q store.Querier) error {
		target, err := targetUser(ctx, q, r, store.StatusPending)
		if err != nil {
			return err
		}
		s.log.Info("registration denied", "ign", target.IGN, "by", actor.IGN)
		return store.DeleteUser(ctx, q, target.ID)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.require(r, store.RoleModerator); err != nil {
		return err
	}
	users, err := store.ListUsers(r.Context(), s.store.DB(), store.StatusActive)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, usersResponse{users})
	return nil
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) error {
	actor, err := s.require(r, store.RoleModerator)
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = s.store.Tx(ctx, func(q store.Querier) error {
		target, err := targetUser(ctx, q, r, store.StatusActive)
		if err != nil {
			return err
		}
		if !outranks(actor, target) {
			return errForbidden("insufficient_rank", "You cannot remove this user")
		}
		s.log.Info("user removed", "ign", target.IGN, "by", actor.IGN)
		return store.DeleteUser(ctx, q, target.ID)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// changeRole: admins can demote moderators to user; only the root admin can set any role. Becoming a moderator
// otherwise goes through a moderator token.
func (s *Server) changeRole(w http.ResponseWriter, r *http.Request) error {
	actor, err := s.require(r, store.RoleAdmin)
	if err != nil {
		return err
	}
	var req struct {
		Role store.Role `json:"role"`
	}
	if err := decodeJSON(w, r, smallBody, &req, false); err != nil {
		return err
	}
	if !req.Role.Valid() {
		return errBadRequest("role must be user, moderator or admin")
	}
	if !actor.Root && req.Role != store.RoleUser {
		return errForbidden("root_only", "Only the root admin can promote users; moderators join with a moderator token")
	}
	ctx := r.Context()
	var user *store.User
	err = s.store.Tx(ctx, func(q store.Querier) error {
		target, err := targetUser(ctx, q, r, store.StatusActive)
		if err != nil {
			return err
		}
		if !outranks(actor, target) {
			return errForbidden("insufficient_rank", "You cannot change this user's role")
		}
		if err := store.SetRole(ctx, q, target.ID, req.Role, false); err != nil {
			return err
		}
		user, err = store.GetUser(ctx, q, target.ID)
		return err
	})
	if err != nil {
		return err
	}
	s.log.Info("role changed", "ign", user.IGN, "role", user.Role, "by", actor.IGN)
	writeJSON(w, http.StatusOK, userResponse{user})
	return nil
}

func (s *Server) signOutUser(w http.ResponseWriter, r *http.Request) error {
	actor, err := s.require(r, store.RoleModerator)
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = s.store.Tx(ctx, func(q store.Querier) error {
		target, err := targetUser(ctx, q, r, store.StatusActive)
		if err != nil {
			return err
		}
		if !outranks(actor, target) {
			return errForbidden("insufficient_rank", "You cannot sign out this user")
		}
		return store.DeleteUserSessions(ctx, q, target.ID, "")
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type tokensResponse struct {
	Tokens []store.OpenToken `json:"tokens"`
}

func (s *Server) listModeratorTokens(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.require(r, store.RoleAdmin); err != nil {
		return err
	}
	tokens, err := store.ListOpenRoleTokens(r.Context(), s.store.DB(), store.RoleModerator, s.nowMs())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, tokensResponse{tokens})
	return nil
}

type issuedToken struct {
	ID        int64  `json:"id"`
	Token     string `json:"token"` // shown once; only its hash is stored
	Note      string `json:"note,omitempty"`
	ExpiresAt int64  `json:"expiresAt"`
}

func (s *Server) createModeratorToken(w http.ResponseWriter, r *http.Request) error {
	actor, err := s.require(r, store.RoleAdmin)
	if err != nil {
		return err
	}
	var req struct {
		Note       string `json:"note"`       // e.g. who the token is for
		ValidHours int    `json:"validHours"` // default 24, at most 168
	}
	if err := decodeJSON(w, r, smallBody, &req, true); err != nil {
		return err
	}
	hours := req.ValidHours
	if hours == 0 {
		hours = defaultTokenHours
	}
	if hours < 1 || hours > maxTokenHours {
		return errBadRequest("validHours must be between 1 and 168")
	}
	note := strings.TrimSpace(req.Note)
	if len(note) > 100 {
		note = note[:100]
	}

	token := secret.NewRoleToken(secret.ModeratorTokenPrefix)
	canonical, _ := secret.NormalizeRoleToken(token)
	now := s.nowMs()
	expires := now + (time.Duration(hours) * time.Hour).Milliseconds()
	ctx := r.Context()
	var id int64
	err = s.store.Tx(ctx, func(q store.Querier) error {
		id, err = store.InsertRoleToken(ctx, q, secret.HashToken(canonical), store.RoleModerator, note, &actor.ID, now, &expires)
		return err
	})
	if err != nil {
		return err
	}
	s.log.Info("moderator token created", "id", id, "by", actor.IGN, "validHours", hours)
	writeJSON(w, http.StatusCreated, issuedToken{ID: id, Token: token, Note: note, ExpiresAt: expires})
	return nil
}

func (s *Server) revokeModeratorToken(w http.ResponseWriter, r *http.Request) error {
	actor, err := s.require(r, store.RoleAdmin)
	if err != nil {
		return err
	}
	id, err := pathID(r)
	if err != nil {
		return err
	}
	deleted, err := store.DeleteUnusedRoleToken(r.Context(), s.store.DB(), id, store.RoleModerator)
	if err != nil {
		return err
	}
	if !deleted {
		return errNotFound("No such unused moderator token")
	}
	s.log.Info("moderator token revoked", "id", id, "by", actor.IGN)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
