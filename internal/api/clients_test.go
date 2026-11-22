package api

import "testing"

func TestAdminsSeeEachUsersApp(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	e.newUser(admin, "Bob", "203.0.113.2") // logs in without naming its app

	// the Java app names itself only at login
	java := e.client("203.0.113.3")
	java.expect(200, "POST", "/api/v1/auth/login", map[string]any{"ign": "Bob", "password": "Bob-password", "client": "Loot Gobbler 0.9.2"})
	rust := e.client("203.0.113.4")
	rust.headers = map[string]string{clientHeader: "rust/0.0.1"}
	rust.login("Hoster", "hoster-password")

	clientOf := func(ign string) string {
		for _, u := range rust.expect(200, "GET", "/api/v1/admin/users", nil).body["users"].([]any) {
			if m := u.(map[string]any); m["ign"] == ign {
				c, _ := m["client"].(string)
				return c
			}
		}
		t.Fatalf("no user %s", ign)
		return ""
	}
	if c := clientOf("Hoster"); c != "Rust v0.0.1" {
		t.Fatalf("Hoster's app: %q", c)
	}
	if c := clientOf("Bob"); c != "Java v0.9.2" { // his latest session
		t.Fatalf("Bob's app: %q", c)
	}
	// updated without logging in again: the next request says so
	rust.headers[clientHeader] = "rust/0.0.2"
	if c := clientOf("Hoster"); c != "Rust v0.0.2" {
		t.Fatalf("after the update: %q", c)
	}
}
