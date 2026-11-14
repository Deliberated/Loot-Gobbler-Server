package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/secret"
)

func userID(c *client) int64 {
	return int64(user(c.expect(200, "GET", "/api/v1/me", nil))["id"].(float64))
}

func TestPasswordResetByModerator(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.20")
	bobID := userID(bob)

	r := admin.expect(201, "POST", "/api/v1/admin/users/"+itoa(bobID)+"/password-reset", nil)
	code := r.body["code"].(string)
	if !strings.HasPrefix(code, "LGR-") || r.body["ign"] != "Bob" {
		t.Fatalf("unexpected reset answer %v", r.body)
	}

	anon := e.client("203.0.113.21")
	// typed loosely: lower case, no dashes, without the prefix
	loose := strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(code, "LGR-"), "-", ""))
	anon.expect(204, "POST", "/api/v1/auth/reset-password", map[string]any{"ign": "bob", "code": loose, "newPassword": "brand-new-pass"})

	// the old session ended, the old password no longer works, the new one does
	bob.expect(401, "GET", "/api/v1/me", nil)
	anon.expect(401, "POST", "/api/v1/auth/login", map[string]any{"ign": "Bob", "password": "Bob-password"})
	anon.login("Bob", "brand-new-pass")

	// single use
	anon.expect(400, "POST", "/api/v1/auth/reset-password", map[string]any{"ign": "Bob", "code": code, "newPassword": "another-pass"})
}

func TestPasswordResetCodeIsBoundToItsPlayerAndReplaced(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.30")
	e.newUser(admin, "Eve", "203.0.113.31")
	bobID := userID(bob)

	first := admin.expect(201, "POST", "/api/v1/admin/users/"+itoa(bobID)+"/password-reset", nil).body["code"].(string)
	second := admin.expect(201, "POST", "/api/v1/admin/users/"+itoa(bobID)+"/password-reset", nil).body["code"].(string)

	anon := e.client("203.0.113.32")
	r := anon.expect(400, "POST", "/api/v1/auth/reset-password", map[string]any{"ign": "Eve", "code": second, "newPassword": "eve-took-it"})
	if r.body["error"] != "invalid_code" {
		t.Fatalf("want invalid_code, got %v", r.body)
	}
	anon.expect(400, "POST", "/api/v1/auth/reset-password", map[string]any{"ign": "Bob", "code": first, "newPassword": "old-code-pass"})
	anon.expect(204, "POST", "/api/v1/auth/reset-password", map[string]any{"ign": "Bob", "code": second, "newPassword": "new-code-pass"})
}

func TestPasswordResetRanks(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	adminID := userID(admin)
	tok := admin.expect(201, "POST", "/api/v1/admin/moderator-tokens", nil).body["token"].(string)
	mod := e.newUser(admin, "Mod", "203.0.113.40")
	mod.expect(200, "POST", "/api/v1/me/redeem", map[string]any{"token": tok})
	mod2tok := admin.expect(201, "POST", "/api/v1/admin/moderator-tokens", nil).body["token"].(string)
	mod2 := e.newUser(admin, "Modtwo", "203.0.113.41")
	mod2.expect(200, "POST", "/api/v1/me/redeem", map[string]any{"token": mod2tok})
	player := e.newUser(admin, "Player", "203.0.113.42")

	mod.expect(201, "POST", "/api/v1/admin/users/"+itoa(userID(player))+"/password-reset", nil)
	mod.expect(403, "POST", "/api/v1/admin/users/"+itoa(userID(mod2))+"/password-reset", nil) // same rank
	mod.expect(403, "POST", "/api/v1/admin/users/"+itoa(adminID)+"/password-reset", nil)
	admin.expect(403, "POST", "/api/v1/admin/users/"+itoa(adminID)+"/password-reset", nil) // not yourself
	player.expect(403, "POST", "/api/v1/admin/users/"+itoa(userID(mod))+"/password-reset", nil)
}

func TestPasswordResetWrongCodesAreLimited(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	e.newUser(admin, "Bob", "203.0.113.50")
	anon := e.client("203.0.113.51")
	body := map[string]any{"ign": "Bob", "code": "LGR-AAAA-BBBB-CCCC", "newPassword": "guessing-pass"}
	for i := 0; i < 5; i++ {
		anon.expect(400, "POST", "/api/v1/auth/reset-password", body)
	}
	anon.expect(429, "POST", "/api/v1/auth/reset-password", body)
}

func TestConsoleResetCodeWorksForRootAdmin(t *testing.T) {
	e := newEnv(t, nil)
	e.newAdmin()
	code, u, err := IssueConsoleResetCode(context.Background(), e.st, "hoster", time.Now())
	if err != nil || u == nil || !u.Root {
		t.Fatalf("console code for the root admin: %v %v", u, err)
	}
	if _, ok := secret.NormalizeResetCode(code); !ok {
		t.Fatalf("bad code %q", code)
	}
	anon := e.client("203.0.113.60")
	anon.expect(204, "POST", "/api/v1/auth/reset-password", map[string]any{"ign": "Hoster", "code": code, "newPassword": "root-is-back"})
	anon.login("Hoster", "root-is-back")

	if _, u, _ := IssueConsoleResetCode(context.Background(), e.st, "Nobody", time.Now()); u != nil {
		t.Fatal("no code for an unknown IGN")
	}
}
