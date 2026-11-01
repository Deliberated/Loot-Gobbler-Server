// Package config reads the server settings from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// Loopback and private ranges: covers nginx proxy manager on the same host or in the same Docker network.
const defaultTrustedProxies = "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7"

type Config struct {
	Port    int
	DataDir string
	// AllowedHosts are the host names (lower case, no port) the server answers for. Empty means any.
	AllowedHosts map[string]bool
	// TrustedProxies may set X-Forwarded-For / X-Real-IP. Requests from anywhere else are taken at face value.
	TrustedProxies []netip.Prefix
	SessionTTL     time.Duration
	// LogLevel: debug also logs every request; warn only problems.
	LogLevel slog.Level
}

func (c Config) Addr() string { return ":" + strconv.Itoa(c.Port) }

func FromEnv() (Config, error) { return FromLookup(os.Getenv) }

// FromLookup builds a Config from get, which returns "" for unset variables.
func FromLookup(get func(string) string) (Config, error) {
	value := func(key, def string) string {
		if v := strings.TrimSpace(get(key)); v != "" {
			return v
		}
		return def
	}

	cfg := Config{DataDir: value("DATA_DIR", "data"), AllowedHosts: map[string]bool{}}

	port, err := strconv.Atoi(value("PORT", "8080"))
	if err != nil || port < 1 || port > 65535 {
		return cfg, fmt.Errorf("PORT must be a number between 1 and 65535")
	}
	cfg.Port = port

	for _, h := range splitList(value("ALLOWED_HOSTS", "")) {
		cfg.AllowedHosts[strings.ToLower(h)] = true
	}

	if proxies := value("TRUSTED_PROXIES", defaultTrustedProxies); !strings.EqualFold(proxies, "none") {
		for _, p := range splitList(proxies) {
			prefix, err := parsePrefix(p)
			if err != nil {
				return cfg, fmt.Errorf("TRUSTED_PROXIES: %q is not an IP address or CIDR range", p)
			}
			cfg.TrustedProxies = append(cfg.TrustedProxies, prefix)
		}
	}

	days, err := strconv.Atoi(value("SESSION_DAYS", "30"))
	if err != nil || days < 1 {
		return cfg, fmt.Errorf("SESSION_DAYS must be a positive number")
	}
	cfg.SessionTTL = time.Duration(days) * 24 * time.Hour

	if err := cfg.LogLevel.UnmarshalText([]byte(value("LOG_LEVEL", "info"))); err != nil {
		return cfg, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error")
	}

	return cfg, nil
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parsePrefix accepts "10.0.0.0/8" as well as a single address like "172.18.0.5".
func parsePrefix(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		return p.Masked(), err
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}
