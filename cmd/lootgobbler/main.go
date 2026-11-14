// Command lootgobbler is the Loot Gobbler server.
//
//	lootgobbler serve        run the server (default)
//	lootgobbler admin-token  print a new admin claim token (invalidates older unused ones)
//	lootgobbler reset-password <IGN>  print a password reset code for that player (any rank, the root admin too)
//	lootgobbler health       exit 0 if the local server answers (Docker health check)
//	lootgobbler version      print the version
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Deliberated/Loot-Gobbler-Server/internal/api"
	"github.com/Deliberated/Loot-Gobbler-Server/internal/config"
	"github.com/Deliberated/Loot-Gobbler-Server/internal/store"
)

// version is set at build time: go build -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	cfg, err := config.FromEnv()
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	switch cmd {
	case "serve":
		err = serve(cfg, log)
	case "admin-token":
		err = adminToken(cfg)
	case "reset-password":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: lootgobbler reset-password <IGN>")
			os.Exit(2)
		}
		err = resetPassword(cfg, os.Args[2])
	case "health":
		err = health(cfg)
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintln(os.Stderr, "usage: lootgobbler [serve | admin-token | reset-password <IGN> | health | version]")
		os.Exit(2)
	}
	if err != nil {
		log.Error(cmd+" failed", "err", err)
		os.Exit(1)
	}
}

func openStore(cfg config.Config) (*store.Store, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, err
	}
	return store.Open(filepath.Join(cfg.DataDir, "lootgobbler.db"))
}

func serve(cfg config.Config, log *slog.Logger) error {
	st, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hasAdmin, err := store.AdminExists(ctx, st.DB())
	if err != nil {
		return err
	}
	if !hasAdmin {
		token, err := api.IssueConsoleAdminToken(ctx, st, time.Now())
		if err != nil {
			return err
		}
		printAdminToken(token)
	}
	if len(cfg.AllowedHosts) == 0 {
		log.Warn("ALLOWED_HOSTS is not set, so the server answers for any host name. Set it to your domain.")
	}

	srv := api.New(api.Options{Config: cfg, Store: st, Logger: log, Version: version})
	go srv.RunMaintenance(ctx)

	httpServer := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.ListenAndServe() }()
	log.Info("Loot Gobbler server started", "version", version, "addr", cfg.Addr(), "data", cfg.DataDir)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func adminToken(cfg config.Config) error {
	st, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	token, err := api.IssueConsoleAdminToken(context.Background(), st, time.Now())
	if err != nil {
		return err
	}
	printAdminToken(token)
	return nil
}

func printAdminToken(token string) {
	fmt.Printf(`
==========================================================================
  ADMIN CLAIM TOKEN (single use)

      %s

  In Loot Gobbler, enter it when registering on this server, or under
  Settings > Server if you already have an account. The first account to
  use it becomes root admin.

  Print a new one (replaces this one):
      docker exec loot-gobbler lootgobbler admin-token
==========================================================================

`, token)
}

func resetPassword(cfg config.Config, ign string) error {
	st, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	code, user, err := api.IssueConsoleResetCode(context.Background(), st, ign, time.Now())
	if err != nil {
		return err
	}
	if user == nil {
		return fmt.Errorf("no active account with the IGN %q", ign)
	}
	fmt.Printf(`
==========================================================================
  PASSWORD RESET CODE for %s (single use, valid %d hours)

      %s

  In Loot Gobbler, on the server login choose "Forgot password?", then
  enter the IGN, this code and a new password. Making a new code
  replaces this one.
==========================================================================

`, user.IGN, int(api.ResetCodeValidity.Hours()), code)
	return nil
}

func health(cfg config.Config) error {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", cfg.Port))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check answered %s", resp.Status)
	}
	return nil
}
