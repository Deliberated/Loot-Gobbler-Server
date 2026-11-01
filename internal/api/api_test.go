package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/config"
	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

type env struct {
	t   *testing.T
	srv *Server
	st  *store.Store
	url string
}

func newEnv(t *testing.T, mutate func(*config.Config)) *env {
	t.Helper()
	cfg, err := config.FromLookup(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(&cfg)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New(Options{Config: cfg, Store: st, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "test"})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return &env{t: t, srv: srv, st: st, url: hs.URL}
}

// client is one player: a session token and the IP the proxy reports for them.
type client struct {
	e     *env
	ip    string
	token string
}

func (e *env) client(ip string) *client { return &client{e: e, ip: ip} }

type response struct {
	status int
	body   map[string]any
	header http.Header
}

func (c *client) do(method, path string, body any) response {
	c.e.t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.url+path, reader)
	req.Header.Set("X-Forwarded-For", c.ip) // the test server sees 127.0.0.1, a trusted proxy by default
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := response{status: resp.StatusCode, header: resp.Header}
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) > 0 {
		json.Unmarshal(raw, &out.body)
	}
	return out
}

func (c *client) expect(want int, method, path string, body any) response {
	c.e.t.Helper()
	r := c.do(method, path, body)
	if r.status != want {
		c.e.t.Fatalf("%s %s: status %d, want %d (%v)", method, path, r.status, want, r.body)
	}
	return r
}

func (c *client) login(ign, password string) {
	c.e.t.Helper()
	r := c.expect(200, "POST", "/api/v1/auth/login", map[string]any{"ign": ign, "password": password})
	c.token = r.body["token"].(string)
}

func user(r response) map[string]any { return r.body["user"].(map[string]any) }

// newAdmin registers "Hoster" with a console admin token and logs in.
func (e *env) newAdmin() *client {
	e.t.Helper()
	token, err := IssueConsoleAdminToken(context.Background(), e.st, time.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	c := e.client("198.51.100.1")
	r := c.expect(201, "POST", "/api/v1/auth/register", map[string]any{"ign": "Hoster", "password": "hoster-password", "token": token})
	if u := user(r); u["role"] != "admin" || u["root"] != true || u["status"] != "active" {
		e.t.Fatalf("console token should make a root admin, got %v", u)
	}
	c.login("Hoster", "hoster-password")
	return c
}

// newUser registers ign, has admin approve it and logs in.
func (e *env) newUser(admin *client, ign, ip string) *client {
	e.t.Helper()
	c := e.client(ip)
	r := c.expect(202, "POST", "/api/v1/auth/register", map[string]any{"ign": ign, "password": ign + "-password"})
	id := int64(user(r)["id"].(float64))
	admin.expect(200, "POST", "/api/v1/admin/requests/"+itoa(id)+"/accept", nil)
	c.login(ign, ign+"-password")
	return c
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestStatusShowsWhetherAdminIsClaimed(t *testing.T) {
	e := newEnv(t, nil)
	anon := e.client("203.0.113.9")
	if r := anon.expect(200, "GET", "/api/v1/status", nil); r.body["adminClaimed"] != false {
		t.Fatalf("fresh server should not have an admin: %v", r.body)
	}
	e.newAdmin()
	if r := anon.expect(200, "GET", "/api/v1/status", nil); r.body["adminClaimed"] != true {
		t.Fatalf("admin should be claimed: %v", r.body)
	}
}

func TestPing(t *testing.T) {
	e := newEnv(t, nil)
	anon := e.client("203.0.113.9")
	r := anon.expect(200, "POST", "/api/v1/ping", nil)
	if r.body["yourIp"] != "203.0.113.9" || r.body["user"] != nil {
		t.Fatalf("anonymous ping: %v", r.body)
	}
	admin := e.newAdmin()
	if u := admin.expect(200, "POST", "/api/v1/ping", nil).body["user"]; u == nil || u.(map[string]any)["ign"] != "Hoster" {
		t.Fatalf("ping with session should name the user, got %v", u)
	}
	expired := e.client("203.0.113.10")
	expired.token = "lgs_not-a-session"
	expired.expect(200, "POST", "/api/v1/ping", nil) // a bad session does not fail the test, it just has no user
	for i := 0; i < 9; i++ {
		anon.do("POST", "/api/v1/ping", nil)
	}
	anon.expect(429, "POST", "/api/v1/ping", nil)
}

func TestRegistrationNeedsApproval(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()

	bob := e.client("203.0.113.2")
	r := bob.expect(202, "POST", "/api/v1/auth/register", map[string]any{"ign": "Bob", "password": "bob-password"})
	id := int64(user(r)["id"].(float64))
	bob.expect(409, "POST", "/api/v1/auth/register", map[string]any{"ign": "bob", "password": "bob-password"})

	r = bob.expect(403, "POST", "/api/v1/auth/login", map[string]any{"ign": "Bob", "password": "bob-password"})
	if r.body["error"] != "pending_approval" {
		t.Fatalf("want pending_approval, got %v", r.body)
	}
	bob.expect(401, "POST", "/api/v1/auth/login", map[string]any{"ign": "Bob", "password": "wrong-password"})

	requests := admin.expect(200, "GET", "/api/v1/admin/requests", nil).body["users"].([]any)
	if len(requests) != 1 {
		t.Fatalf("want 1 pending request, got %v", requests)
	}
	admin.expect(200, "POST", "/api/v1/admin/requests/"+itoa(id)+"/accept", nil)
	bob.login("bob", "bob-password") // IGNs are case-insensitive
	if u := user(bob.expect(200, "GET", "/api/v1/me", nil)); u["ign"] != "Bob" || u["role"] != "user" {
		t.Fatalf("unexpected user %v", u)
	}

	// A denied request is removed, so the IGN can register again.
	eve := e.client("203.0.113.3")
	r = eve.expect(202, "POST", "/api/v1/auth/register", map[string]any{"ign": "Eve", "password": "eve-password"})
	admin.expect(204, "POST", "/api/v1/admin/requests/"+itoa(int64(user(r)["id"].(float64)))+"/deny", nil)
	eve.expect(401, "POST", "/api/v1/auth/login", map[string]any{"ign": "Eve", "password": "eve-password"})
}

func TestModeratorToken(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")

	bob.expect(403, "POST", "/api/v1/admin/moderator-tokens", nil) // users cannot make tokens
	r := admin.expect(201, "POST", "/api/v1/admin/moderator-tokens", map[string]any{"note": "for Bob"})
	token := r.body["token"].(string)
	if open := admin.expect(200, "GET", "/api/v1/admin/moderator-tokens", nil).body["tokens"].([]any); len(open) != 1 {
		t.Fatalf("want 1 open token, got %v", open)
	}

	if u := user(bob.expect(200, "POST", "/api/v1/me/redeem", map[string]any{"token": token})); u["role"] != "moderator" {
		t.Fatalf("want moderator, got %v", u)
	}
	// Single use.
	carl := e.newUser(admin, "Carl", "203.0.113.4")
	carl.expect(400, "POST", "/api/v1/me/redeem", map[string]any{"token": token})

	// Moderators approve registrations and remove plain users, but cannot touch admins or make tokens.
	dave := e.client("203.0.113.5")
	r = dave.expect(202, "POST", "/api/v1/auth/register", map[string]any{"ign": "Dave", "password": "dave-password"})
	bob.expect(200, "POST", "/api/v1/admin/requests/"+itoa(int64(user(r)["id"].(float64)))+"/accept", nil)
	bob.expect(403, "POST", "/api/v1/admin/moderator-tokens", nil)
	adminID := int64(user(admin.expect(200, "GET", "/api/v1/me", nil))["id"].(float64))
	bob.expect(403, "DELETE", "/api/v1/admin/users/"+itoa(adminID), nil)
	carlID := int64(user(carl.expect(200, "GET", "/api/v1/me", nil))["id"].(float64))
	bob.expect(204, "DELETE", "/api/v1/admin/users/"+itoa(carlID), nil)
	carl.expect(401, "GET", "/api/v1/me", nil)

	// A new player can register with a moderator token and skip the approval queue.
	r = admin.expect(201, "POST", "/api/v1/admin/moderator-tokens", nil)
	frank := e.client("203.0.113.6")
	r = frank.expect(201, "POST", "/api/v1/auth/register", map[string]any{"ign": "Frank", "password": "frank-password", "token": r.body["token"]})
	if u := user(r); u["role"] != "moderator" || u["status"] != "active" {
		t.Fatalf("want active moderator, got %v", u)
	}

	// Admin demotes Bob; the root admin itself can never be removed.
	bobID := int64(user(bob.expect(200, "GET", "/api/v1/me", nil))["id"].(float64))
	admin.expect(200, "PATCH", "/api/v1/admin/users/"+itoa(bobID)+"/role", map[string]any{"role": "user"})
	admin.expect(403, "DELETE", "/api/v1/admin/users/"+itoa(adminID), nil)
	admin.expect(403, "DELETE", "/api/v1/me", map[string]any{"password": "hoster-password"})
}

func TestSecondAdminTokenIsNotRoot(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	token, err := IssueConsoleAdminToken(context.Background(), e.st, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if u := user(bob.expect(200, "POST", "/api/v1/me/redeem", map[string]any{"token": token})); u["role"] != "admin" || u["root"] == true {
		t.Fatalf("want non-root admin, got %v", u)
	}
	// A non-root admin cannot remove the root admin or promote others.
	adminID := int64(user(admin.expect(200, "GET", "/api/v1/me", nil))["id"].(float64))
	bob.expect(403, "DELETE", "/api/v1/admin/users/"+itoa(adminID), nil)
	// Root can demote Bob again.
	bobID := int64(user(bob.expect(200, "GET", "/api/v1/me", nil))["id"].(float64))
	admin.expect(200, "PATCH", "/api/v1/admin/users/"+itoa(bobID)+"/role", map[string]any{"role": "moderator"})
}

func TestNewConsoleTokenInvalidatesOldOne(t *testing.T) {
	e := newEnv(t, nil)
	old, _ := IssueConsoleAdminToken(context.Background(), e.st, time.Now())
	IssueConsoleAdminToken(context.Background(), e.st, time.Now())
	c := e.client("203.0.113.2")
	c.expect(400, "POST", "/api/v1/auth/register", map[string]any{"ign": "Hoster", "password": "hoster-password", "token": old})
}

func TestTokenAttemptsAreLimitedPerIP(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	good := admin.expect(201, "POST", "/api/v1/admin/moderator-tokens", nil).body["token"].(string)

	bad := "LGM-0000-0000-0000-0000-0000-0000-0000-0000"
	for i := 0; i < 3; i++ {
		bob.expect(400, "POST", "/api/v1/me/redeem", map[string]any{"token": bad})
	}
	r := bob.expect(429, "POST", "/api/v1/me/redeem", map[string]any{"token": good}) // even the right token waits
	if r.header.Get("Retry-After") == "" {
		t.Fatal("429 should carry Retry-After")
	}
	// Registering with a token from the same IP is blocked too.
	stranger := e.client("203.0.113.2")
	stranger.expect(429, "POST", "/api/v1/auth/register", map[string]any{"ign": "Eve", "password": "eve-password", "token": good})
}

func TestTokenAttemptsAreLimitedPerAccount(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	bad := "LGM-0000-0000-0000-0000-0000-0000-0000-0000"
	for i, ip := range []string{"203.0.113.10", "203.0.113.11", "203.0.113.12"} {
		bob.ip = ip
		if r := bob.do("POST", "/api/v1/me/redeem", map[string]any{"token": bad}); r.status != 400 {
			t.Fatalf("attempt %d: want 400, got %d", i, r.status)
		}
	}
	bob.ip = "203.0.113.13"
	bob.expect(429, "POST", "/api/v1/me/redeem", map[string]any{"token": bad})
}

func TestLoginLimit(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	e.newUser(admin, "Bob", "203.0.113.2")
	attacker := e.client("203.0.113.66")
	for i := 0; i < 5; i++ {
		attacker.expect(401, "POST", "/api/v1/auth/login", map[string]any{"ign": "Bob", "password": "guess-" + itoa(int64(i))})
	}
	attacker.expect(429, "POST", "/api/v1/auth/login", map[string]any{"ign": "Bob", "password": "Bob-password"})
	// Bob, from his own IP, is not locked out.
	bob := e.client("203.0.113.2")
	bob.login("Bob", "Bob-password")
}

func TestForwardedForCannotBeSpoofedFromOutside(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.TrustedProxies = nil }) // the test client is now an untrusted address
	srv := e.srv
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if ip := srv.clientIP(req); ip != "203.0.113.7" {
		t.Fatalf("untrusted client should not pick its IP, got %s", ip)
	}
	srv.cfg, _ = config.FromLookup(func(string) string { return "" })
	req.RemoteAddr = "172.18.0.2:5555" // nginx proxy manager in Docker
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.7")
	if ip := srv.clientIP(req); ip != "203.0.113.7" {
		t.Fatalf("want the address the proxy saw, got %s", ip)
	}
}

func TestAllowedHosts(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.AllowedHosts = map[string]bool{"loot.example.com": true} })
	get := func(host, path string) int {
		req, _ := http.NewRequest("GET", e.url+path, nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if s := get("evil.example.com", "/api/v1/status"); s != http.StatusMisdirectedRequest {
		t.Fatalf("unknown host: want 421, got %d", s)
	}
	if s := get("loot.example.com:443", "/api/v1/status"); s != 200 {
		t.Fatalf("allowed host: want 200, got %d", s)
	}
	if s := get("127.0.0.1:8080", "/api/v1/health"); s != 200 {
		t.Fatalf("health check must work on any host, got %d", s)
	}
}

func TestDataSync(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	pc1 := e.newUser(admin, "Bob", "203.0.113.2")
	pc2 := e.client("203.0.113.3")
	pc2.login("Bob", "Bob-password")

	r := pc1.expect(200, "PUT", "/api/v1/me/data/collection", map[string]any{"baseRevision": 0, "data": map[string]any{"14410": map[string]any{"count": 1}}})
	if r.body["revision"] != 1.0 {
		t.Fatalf("want revision 1, got %v", r.body)
	}

	// pc2 still thinks the document does not exist: conflict, and it gets the current version to merge.
	r = pc2.expect(409, "PUT", "/api/v1/me/data/collection", map[string]any{"baseRevision": 0, "data": map[string]any{}})
	current := r.body["current"].(map[string]any)
	if current["revision"] != 1.0 || current["data"].(map[string]any)["14410"] == nil {
		t.Fatalf("conflict should carry the stored document, got %v", r.body)
	}
	pc2.expect(200, "PUT", "/api/v1/me/data/collection", map[string]any{"baseRevision": 1, "data": map[string]any{"14410": map[string]any{"count": 2}}})

	meta := pc1.expect(200, "GET", "/api/v1/me/data?meta=true", nil).body["items"].(map[string]any)["collection"].(map[string]any)
	if meta["revision"] != 2.0 || meta["data"] != nil {
		t.Fatalf("meta should have revision 2 and no data, got %v", meta)
	}
	full := pc1.expect(200, "GET", "/api/v1/me/data?kinds=collection", nil).body["items"].(map[string]any)["collection"].(map[string]any)
	if full["data"].(map[string]any)["14410"].(map[string]any)["count"] != 2.0 {
		t.Fatalf("unexpected data %v", full)
	}

	// Other players do not see Bob's data.
	carl := e.newUser(admin, "Carl", "203.0.113.4")
	if items := carl.expect(200, "GET", "/api/v1/me/data", nil).body["items"].(map[string]any); len(items) != 0 {
		t.Fatalf("Carl should have no data, got %v", items)
	}

	pc1.expect(400, "PUT", "/api/v1/me/data/Not-Valid", map[string]any{"baseRevision": 0, "data": 1})
	pc1.expect(400, "PUT", "/api/v1/me/data/pets", map[string]any{"data": 1})
}

// put uploads a new document for the client's account.
func (c *client) put(kind string, data any) {
	c.e.t.Helper()
	c.expect(200, "PUT", "/api/v1/me/data/"+kind, map[string]any{"baseRevision": 0, "data": data})
}

func TestGuildViews(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	carl := e.newUser(admin, "Carl", "203.0.113.3")
	dave := e.newUser(admin, "Dave", "203.0.113.4") // another guild

	bob.expect(409, "GET", "/api/v1/guild", nil) // no profile yet

	admin.put("profile", map[string]any{"ign": "Hoster", "guild": "Gobblers", "guildRank": 40})
	bob.put("profile", map[string]any{"ign": "Bob", "guild": "gobblers", "guildRank": 10}) // case doesn't matter
	carl.put("profile", map[string]any{"ign": "Carl", "guild": "Gobblers", "guildRank": 20})
	dave.put("profile", map[string]any{"ign": "Dave", "guild": "Others", "guildRank": 40})

	find := func(time, source string) map[string]any {
		return map[string]any{"time": time, "source": source, "enchants": 1, "where": "Lost Halls", "bag": "white"}
	}
	bob.put("collection", map[string]any{
		"100": map[string]any{"count": 2, "history": []any{find("2026-11-01T10:00:00", "drop"), find("2026-11-01T12:00:00", "vault")}},
		"200": map[string]any{"count": 1, "history": []any{find("2026-11-01T11:00:00", "drop")}},
	})
	carl.put("collection", map[string]any{
		"100": map[string]any{"count": 1, "history": []any{find("2026-11-01T13:00:00", "drop")}},
	})
	dave.put("collection", map[string]any{
		"300": map[string]any{"count": 1, "history": []any{find("2026-11-01T14:00:00", "drop")}},
	})
	bob.put("characters", map[string]any{
		"1": map[string]any{"charId": 1, "fame": 1000, "alive": true, "statsMaxed": 8, "samples": []any{map[string]any{"t": 1, "fame": 1}}},
		"2": map[string]any{"charId": 2, "fame": 500, "alive": false},
	})
	bob.put("exalts", map[string]any{"805": []int{75, 75, 5, 0, 0, 0, 0, 0}}) // 5 + 5 + 1

	g := bob.expect(200, "GET", "/api/v1/guild", nil).body
	if g["guild"] != "Gobblers" || len(g["members"].([]any)) != 3 {
		t.Fatalf("guild: %v", g)
	}

	lb := bob.expect(200, "GET", "/api/v1/guild/leaderboard", nil).body["members"].([]any)
	first := lb[0].(map[string]any)
	if first["ign"] != "Bob" || first["items"] != 2.0 || first["finds"] != 3.0 || first["fame"] != 1000.0 ||
		first["exalts"] != 11.0 || first["characters"] != 1.0 || first["maxed"] != 1.0 {
		t.Fatalf("Bob's leaderboard row: %v", first)
	}
	for _, row := range lb {
		if row.(map[string]any)["ign"] == "Dave" {
			t.Fatal("Dave is in another guild")
		}
	}

	drops := carl.expect(200, "GET", "/api/v1/guild/drops", nil).body["drops"].([]any)
	if len(drops) != 3 { // Bob's two drops and Carl's one; Bob's vault sighting and Dave's drop are not shown
		t.Fatalf("drops: %v", drops)
	}
	if d := drops[0].(map[string]any); d["ign"] != "Carl" || d["itemId"] != 100.0 {
		t.Fatalf("newest drop should be Carl's: %v", d)
	}
	if one := carl.expect(200, "GET", "/api/v1/guild/drops?limit=1", nil).body["drops"].([]any); len(one) != 1 {
		t.Fatalf("limit=1 gave %d", len(one))
	}

	bobID := itoa(int64(first["id"].(float64)))
	m := carl.expect(200, "GET", "/api/v1/guild/members/"+bobID, nil).body
	chars := m["characters"].(map[string]any)
	if _, hasSamples := chars["1"].(map[string]any)["samples"]; hasSamples || len(chars) != 2 {
		t.Fatalf("member characters should be there without samples: %v", chars)
	}
	if m["collection"].(map[string]any)["100"] != 2.0 {
		t.Fatalf("member collection: %v", m["collection"])
	}
	dave.expect(404, "GET", "/api/v1/guild/members/"+bobID, nil) // not Dave's guild
}

func TestGuildCaravan(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	bob := e.newUser(admin, "Bob", "203.0.113.2")
	admin.put("profile", map[string]any{"ign": "Hoster", "guild": "Gobblers", "guildRank": 40})
	bob.put("profile", map[string]any{"ign": "Bob", "guild": "Gobblers", "guildRank": 10})

	now := time.Now().UTC()
	at := func(ago time.Duration) string { return now.Add(-ago).Format("2006-01-02T15:04:05") }
	find := func(time, source, bag string) map[string]any {
		return map[string]any{"time": time, "source": source, "where": "Lost Halls", "bag": bag}
	}
	same := at(time.Hour) // two items out of one white bag
	bob.put("collection", map[string]any{
		"100": map[string]any{"count": 3, "history": []any{find(same, "drop", "White"), find(at(40*24*time.Hour), "drop", "White"),
			find(at(2*time.Hour), "vault", "")}},
		"200": map[string]any{"count": 2, "history": []any{find(same, "drop", "White"), find(at(3*time.Hour), "drop", "Boosted Orange")}},
	})
	day := time.Hour.Milliseconds() * 24
	ms := now.UnixMilli()
	bob.put("characters", map[string]any{
		"1": map[string]any{"classType": 782, "className": "Wizard", "alive": true, "lastPlayed": ms,
			"samples": []any{map[string]any{"t": ms - day, "fame": 100}, map[string]any{"t": ms, "fame": 150}}},
		"2": map[string]any{"classType": 797, "className": "Knight", "alive": true, "lastPlayed": ms - day,
			"samples": []any{map[string]any{"t": ms - 2*day, "fame": 0}, map[string]any{"t": ms - day, "fame": 900}}},
	})

	week := bob.expect(200, "GET", "/api/v1/guild/caravan", nil).body
	members := week["members"].([]any)
	first := members[0].(map[string]any)
	bags := first["bags"].(map[string]any)
	if week["range"] != "week" || len(members) != 2 || first["ign"] != "Bob" || first["total"] != 2.0 ||
		bags["White"] != 1.0 || bags["Orange"] != 1.0 || len(first["items"].([]any)) != 3 {
		t.Fatalf("week: %v", week)
	}
	if c := first["character"].(map[string]any); c["className"] != "Knight" { // gained 900 vs 50 this week
		t.Fatalf("most played: %v", c)
	}
	if hoster := members[1].(map[string]any); hoster["total"] != 0.0 || hoster["character"] != nil {
		t.Fatalf("member without data: %v", hoster)
	}
	all := bob.expect(200, "GET", "/api/v1/guild/caravan?range=all", nil).body["members"].([]any)[0].(map[string]any)
	if all["total"] != 3.0 {
		t.Fatalf("all time: %v", all)
	}
	bob.expect(400, "GET", "/api/v1/guild/caravan?range=year", nil)
}

func TestPasswordChangeSignsOutOtherSessions(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.newAdmin()
	pc1 := e.newUser(admin, "Bob", "203.0.113.2")
	pc2 := e.client("203.0.113.3")
	pc2.login("Bob", "Bob-password")

	pc1.expect(403, "PATCH", "/api/v1/me/password", map[string]any{"currentPassword": "wrong", "newPassword": "new-password"})
	pc1.expect(204, "PATCH", "/api/v1/me/password", map[string]any{"currentPassword": "Bob-password", "newPassword": "new-password"})
	pc1.expect(200, "GET", "/api/v1/me", nil)
	pc2.expect(401, "GET", "/api/v1/me", nil)
	pc2.login("Bob", "new-password")

	pc2.expect(204, "POST", "/api/v1/auth/logout", nil)
	pc2.expect(401, "GET", "/api/v1/me", nil)
}
