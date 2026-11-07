package api

import "testing"

func TestGuildSettings(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")

	if g := bob.expect(200, "GET", "/api/v1/guild/settings", nil).body; g["baselineHpm"] != float64(50) {
		t.Fatalf("default guild settings: %v", g)
	}
	bob.expect(403, "PUT", "/api/v1/admin/guild/settings", map[string]any{"baselineHpm": 40})
	admin.expect(400, "PUT", "/api/v1/admin/guild/settings", map[string]any{"baselineHpm": 0})
	admin.expect(400, "PUT", "/api/v1/admin/guild/settings", map[string]any{})
	admin.expect(200, "PUT", "/api/v1/admin/guild/settings", map[string]any{"baselineHpm": 35.5})
	if g := bob.expect(200, "GET", "/api/v1/guild/settings", nil).body; g["baselineHpm"] != 35.5 {
		t.Fatalf("stored guild settings: %v", g)
	}
}
