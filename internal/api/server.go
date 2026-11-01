// Package api is the HTTP/JSON API the Loot Gobbler client talks to. See docs/API.md.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/config"
	"github.com/Deliberated/Loot-Gobbler-Server/internal/ratelimit"
	"github.com/Deliberated/Loot-Gobbler-Server/internal/secret"
	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// APIVersion changes only on breaking changes; the path prefix /api/v1 follows it.
const APIVersion = 1

const (
	smallBody = 64 << 10 // logins, tokens, admin actions
	dataBody  = 16 << 20 // data uploads (collection, DPS history, ...)
)

type Options struct {
	Config  config.Config
	Store   *store.Store
	Logger  *slog.Logger
	Version string
}

type Server struct {
	cfg     config.Config
	store   *store.Store
	log     *slog.Logger
	version string
	limits  *limits
	now     func() time.Time
	mux     *http.ServeMux
}

// limits are the brute-force counters. Only failures count, except for registrations without a token.
type limits struct {
	loginIP      *ratelimit.Limiter // failed logins per IP
	loginAccount *ratelimit.Limiter // failed logins per IP + IGN
	register     *ratelimit.Limiter // registration requests per IP
	tokenIP      *ratelimit.Limiter // wrong role tokens per IP
	tokenUser    *ratelimit.Limiter // wrong role tokens per account
	password     *ratelimit.Limiter // wrong current password per account
	ping         *ratelimit.Limiter // connection tests per IP (each one is logged)
}

func newLimits() *limits {
	return &limits{
		loginIP:      ratelimit.New(20, 15*time.Minute),
		loginAccount: ratelimit.New(5, 15*time.Minute),
		register:     ratelimit.New(3, time.Hour),
		tokenIP:      ratelimit.New(3, time.Hour),
		tokenUser:    ratelimit.New(3, time.Hour),
		password:     ratelimit.New(5, 15*time.Minute),
		ping:         ratelimit.New(10, time.Minute),
	}
}

func (l *limits) all() []*ratelimit.Limiter {
	return []*ratelimit.Limiter{l.loginIP, l.loginAccount, l.register, l.tokenIP, l.tokenUser, l.password, l.ping}
}

func New(opts Options) *Server {
	s := &Server{
		cfg:     opts.Config,
		store:   opts.Store,
		log:     opts.Logger,
		version: opts.Version,
		limits:  newLimits(),
		now:     time.Now,
		mux:     http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.handle("GET /api/v1/health", s.health)
	s.handle("GET /api/v1/status", s.status)
	s.handle("POST /api/v1/ping", s.ping)

	s.handle("POST /api/v1/auth/register", s.register)
	s.handle("POST /api/v1/auth/login", s.login)
	s.handle("POST /api/v1/auth/logout", s.logout)

	s.handle("GET /api/v1/me", s.me)
	s.handle("PATCH /api/v1/me/password", s.changePassword)
	s.handle("DELETE /api/v1/me", s.deleteAccount)
	s.handle("POST /api/v1/me/redeem", s.redeem)
	s.handle("GET /api/v1/me/data", s.getData)
	s.handle("PUT /api/v1/me/data/{kind}", s.putData)

	s.handle("GET /api/v1/admin/requests", s.listRequests)
	s.handle("POST /api/v1/admin/requests/{id}/accept", s.acceptRequest)
	s.handle("POST /api/v1/admin/requests/{id}/deny", s.denyRequest)
	s.handle("GET /api/v1/admin/users", s.listUsers)
	s.handle("DELETE /api/v1/admin/users/{id}", s.deleteUser)
	s.handle("PATCH /api/v1/admin/users/{id}/role", s.changeRole)
	s.handle("DELETE /api/v1/admin/users/{id}/sessions", s.signOutUser)
	s.handle("GET /api/v1/admin/moderator-tokens", s.listModeratorTokens)
	s.handle("POST /api/v1/admin/moderator-tokens", s.createModeratorToken)
	s.handle("DELETE /api/v1/admin/moderator-tokens/{id}", s.revokeModeratorToken)

	s.handle("/", func(w http.ResponseWriter, r *http.Request) error { return errNotFound("No such endpoint") })
}

// Handler is the complete HTTP handler, including the host name check.
func (s *Server) Handler() http.Handler { return s.requestLog(s.hostCheck(s.mux)) }

// requestLog logs every request at debug level (LOG_LEVEL=debug); otherwise it adds nothing.
func (s *Server) requestLog(next http.Handler) http.Handler {
	if !s.log.Enabled(context.Background(), slog.LevelDebug) {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/api/v1/health" && rec.status == http.StatusOK {
			return // the Docker health check, every 30 seconds
		}
		s.log.Debug("request", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"ms", time.Since(start).Milliseconds(), "ip", s.clientIP(r), "bytesIn", r.ContentLength)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// RunMaintenance clears expired sessions and rate limit counters until ctx is done.
func (s *Server) RunMaintenance(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, l := range s.limits.all() {
				l.Sweep()
			}
			if _, err := store.PurgeExpiredSessions(ctx, s.store.DB(), s.nowMs()); err != nil && ctx.Err() == nil {
				s.log.Error("purging expired sessions failed", "err", err)
			}
		}
	}
}

// IssueConsoleAdminToken creates a new admin claim token and invalidates unused older ones.
func IssueConsoleAdminToken(ctx context.Context, st *store.Store, now time.Time) (string, error) {
	token := secret.NewRoleToken(secret.AdminTokenPrefix)
	canonical, _ := secret.NormalizeRoleToken(token)
	err := st.Tx(ctx, func(q store.Querier) error {
		if err := store.DeleteUnusedConsoleTokens(ctx, q); err != nil {
			return err
		}
		_, err := store.InsertRoleToken(ctx, q, secret.HashToken(canonical), store.RoleAdmin, "console", nil, now.UnixMilli(), nil)
		return err
	})
	return token, err
}

func (s *Server) handle(pattern string, h func(http.ResponseWriter, *http.Request) error) {
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			s.writeError(w, r, err)
		}
	})
}

func (s *Server) nowMs() int64 { return s.now().UnixMilli() }

// --- errors and JSON ---

type apiError struct {
	Status     int    `json:"-"`
	Code       string `json:"error"`
	Message    string `json:"message"`
	RetryAfter int64  `json:"retryAfter,omitempty"` // seconds
	Current    any    `json:"current,omitempty"`    // the stored document, on revision conflicts
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }

func errBadRequest(msg string) error {
	return &apiError{Status: http.StatusBadRequest, Code: "bad_request", Message: msg}
}
func errNotFound(msg string) error {
	return &apiError{Status: http.StatusNotFound, Code: "not_found", Message: msg}
}
func errUnauthorized(code, msg string) error {
	return &apiError{Status: http.StatusUnauthorized, Code: code, Message: msg}
}
func errForbidden(code, msg string) error {
	return &apiError{Status: http.StatusForbidden, Code: code, Message: msg}
}
func errConflict(code, msg string) error {
	return &apiError{Status: http.StatusConflict, Code: code, Message: msg}
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		if ae.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.FormatInt(ae.RetryAfter, 10))
		}
		writeJSON(w, ae.Status, ae)
		return
	}
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeJSON(w, http.StatusInternalServerError, &apiError{Code: "internal", Message: "Internal server error"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// decodeJSON reads a JSON body of at most limit bytes into v. With optional, an empty body leaves v unchanged.
func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, v any, optional bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	err := json.NewDecoder(r.Body).Decode(v)
	var tooLarge *http.MaxBytesError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &tooLarge):
		return &apiError{Status: http.StatusRequestEntityTooLarge, Code: "too_large",
			Message: "Request body is larger than " + strconv.FormatInt(limit>>10, 10) + " KiB"}
	case errors.Is(err, io.EOF) && optional:
		return nil
	case errors.Is(err, io.EOF):
		return errBadRequest("Request body is missing")
	default:
		return errBadRequest("Request body is not valid JSON")
	}
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, errNotFound("No such id")
	}
	return id, nil
}

// checkLimit turns a tripped limiter into a 429 response.
func checkLimit(l *ratelimit.Limiter, key string) error {
	retry, ok := l.Check(key)
	if ok {
		return nil
	}
	secs := int64((retry + time.Second - 1) / time.Second)
	return &apiError{Status: http.StatusTooManyRequests, Code: "rate_limited",
		Message: "Too many failed attempts, try again later", RetryAfter: max(secs, 1)}
}

// --- proxy and host handling ---

// clientIP is the address the request came from. Behind a trusted proxy (nginx proxy manager), it is the rightmost
// X-Forwarded-For entry the proxies did not add themselves; a client cannot fake it by sending its own header.
func (s *Server) clientIP(r *http.Request) string {
	remote := remoteAddr(r)
	if !remote.IsValid() {
		return r.RemoteAddr
	}
	if !s.trusted(remote) {
		return remote.String()
	}
	if values := r.Header.Values("X-Forwarded-For"); len(values) > 0 {
		hops := strings.Split(strings.Join(values, ","), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			addr, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
			if err != nil {
				break
			}
			if addr = addr.Unmap(); !s.trusted(addr) || i == 0 {
				return addr.String()
			}
		}
		return remote.String()
	}
	if addr, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
		return addr.Unmap().String()
	}
	return remote.String()
}

func remoteAddr(r *http.Request) netip.Addr {
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return ap.Addr().Unmap()
	}
	addr, _ := netip.ParseAddr(r.RemoteAddr)
	return addr.Unmap()
}

func (s *Server) trusted(addr netip.Addr) bool {
	for _, p := range s.cfg.TrustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// hostCheck rejects requests for host names other than ALLOWED_HOSTS. The health endpoint stays open for the
// Docker health check, which calls 127.0.0.1.
func (s *Server) hostCheck(next http.Handler) http.Handler {
	if len(s.cfg.AllowedHosts) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" || s.cfg.AllowedHosts[hostOnly(r.Host)] {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusMisdirectedRequest, &apiError{Code: "unknown_host", Message: "This server does not answer for that host name"})
	})
}

func hostOnly(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return strings.ToLower(strings.Trim(host, "[]"))
}

// --- public endpoints ---

func (s *Server) health(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

type statusResponse struct {
	Service      string `json:"service"`
	Version      string `json:"version"`
	APIVersion   int    `json:"apiVersion"`
	AdminClaimed bool   `json:"adminClaimed"`
	Registration string `json:"registration"` // "approval": new accounts wait for a moderator or admin
}

type pingResponse struct {
	OK         bool        `json:"ok"`
	ServerTime int64       `json:"serverTime"`
	YourIP     string      `json:"yourIp"`         // as the server sees it: shows whether the proxy passes the real IP
	User       *store.User `json:"user,omitempty"` // if a valid session was sent
}

// ping is the client's "Test connection" button. It is logged, so the host sees tests arrive in the console.
func (s *Server) ping(w http.ResponseWriter, r *http.Request) error {
	ip := s.clientIP(r)
	if err := checkLimit(s.limits.ping, ip); err != nil {
		return err
	}
	s.limits.ping.Record(ip)
	resp := pingResponse{OK: true, ServerTime: s.nowMs(), YourIP: ip}
	if r.Header.Get("Authorization") != "" {
		if user, _, err := s.principal(r); err == nil {
			resp.User = user
		}
	}
	if resp.User != nil {
		s.log.Info("connection test", "ip", ip, "ign", resp.User.IGN)
	} else {
		s.log.Info("connection test", "ip", ip)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) error {
	claimed, err := store.AdminExists(r.Context(), s.store.DB())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, statusResponse{
		Service: "loot-gobbler-server", Version: s.version, APIVersion: APIVersion,
		AdminClaimed: claimed, Registration: "approval",
	})
	return nil
}
