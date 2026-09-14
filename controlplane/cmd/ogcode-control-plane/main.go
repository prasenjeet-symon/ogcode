// Command ogcode-control-plane is the master/orchestrator daemon. Workers (the
// `ogcode worker` run-mode of ogcode) dial it over ConnectRPC; the operator
// panel talks to it over HTTP. It is a separate binary/repo from ogcode by
// design — the worker is the only piece that must live in ogcode.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/prasenjeet-symon/ogcode-control-plane/internal/auth"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/bus"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/config"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/master"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/tlsreload"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "ogcode-control-plane",
		Short:         "ogcode control plane (master/orchestrator for remote agent workers)",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.AddCommand(serveCmd())
	root.AddCommand(usersCmd())
	return root
}

func serveCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the control-plane server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return serve(cmd.Context(), configPath)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "control-plane.json", "path to the control-plane config file")
	return cmd
}

func serve(ctx context.Context, configPath string) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	// Durable registry. The bbolt file (0600) is the write-behind for worker +
	// session state; on boot the registry is restored with live state reset so a
	// restart never looks like a mass worker death. DBPath empty stays fully
	// in-memory.
	store, err := registry.Open(cfg.Master.DBFile())
	recoveredCorrupt := false
	if err != nil {
		if !errors.Is(err, registry.ErrCorrupt) {
			return fmt.Errorf("open control-plane store: %w", err)
		}
		// A2 whole-file corrupt policy: move the bad file aside (forensic copy)
		// and start with a FRESH store so workers re-register and the master keeps
		// serving. Registry is self-healing (workers re-register); the accounts
		// table is gone, so the operator gate flips to locked — never opened.
		logger.Error("control-plane DB is corrupt; setting it aside and re-seeding an empty store (operator accounts must be re-created)",
			"err", err, "db", cfg.Master.DBFile())
		moveAside(cfg.Master.DBFile(), logger)
		store, err = registry.Open(cfg.Master.DBFile())
		if err != nil {
			return fmt.Errorf("re-seed control-plane store: %w", err)
		}
		recoveredCorrupt = true
	}
	defer store.Close()

	reg := registry.NewWithStore(store, logger)
	if snap, lerr := store.Load(); lerr != nil {
		// A corrupt registry bucket is recoverable: start empty and let workers
		// re-register themselves. Move the bad file aside so it can be inspected
		// (the next Open recreates a fresh DB); the master keeps serving with an
		// empty registry rather than failing to boot.
		logger.Error("control-plane registry failed to load; starting empty and re-registering workers",
			"err", lerr, "db", store.Path())
		moveAside(store.Path(), logger)
	} else {
		bootTime := time.Now()
		reg.Restore(snap, bootTime)
		logger.Info("control-plane registry restored",
			"workers", len(snap.Workers), "sessions", len(snap.Sessions), "db", store.Path())
	}
	pairAuth := pairing.New(cfg.Master.PairingSecret, cfg.Master.TokenTTL())
	eventBus := bus.New(0)

	// Operator login gate for the browser-facing UI proxy. Mode resolution:
	// any account in the users bucket wins (accounts); else a configured
	// operatorPassword gives the legacy single-password mode; neither runs open
	// (dev, logged loudly). If the store was re-seeded after a corrupt file, the
	// accounts are gone — the gate is LOCKED (demands a session, authenticates
	// nobody) until an operator re-creates accounts and restarts.
	var gate *auth.Gate
	if recoveredCorrupt {
		g, err := auth.NewLockedGate(cfg.Master.SessionTTL(), cfg.Master.CookieDomain, cfg.Master.TLS != nil)
		if err != nil {
			return err
		}
		gate = g
	} else {
		g, err := auth.NewGate(store, cfg.Master.OperatorPassword, cfg.Master.SessionTTL(), cfg.Master.CookieDomain, cfg.Master.TLS != nil)
		if err != nil {
			return err
		}
		if g.Enabled() {
			gate = g
			if g.Mode() == auth.ModeLegacy {
				logger.Warn("master.operatorPassword is the legacy single-password gate; add per-employee accounts with `ogcode-control-plane users add` before it is removed",
					"see", "control-plane.example.json")
			}
		} else {
			logger.Warn("no operator accounts and no master.operatorPassword set — the worker UI proxy is UNAUTHENTICATED; seed accounts with `ogcode-control-plane users add` before exposing the control plane")
		}
	}

	srv := master.New(master.Options{
		Registry: reg,
		Auth:     pairAuth,
		Bus:      eventBus,
		Logger:   logger,
		Gate:     gate,
	})

	mux := http.NewServeMux()
	path, handler := srv.Handler()
	mux.Handle(path, handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// A request whose host names a connected worker (e.g. <workerID>.<host>) is
	// reverse-proxied to that worker's tunneled ogcode UI; everything else (the
	// ConnectRPC endpoints, health) falls through to the mux.
	rootHandler := srv.UIProxyHandler(mux)

	httpSrv := &http.Server{
		Addr:              cfg.Master.Listen,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// ConnectRPC bidi streaming requires HTTP/2. With TLS, net/http negotiates
	// h2 via ALPN. Without TLS (local dev), wrap the mux in an h2c handler so the
	// bidi stream still works over cleartext HTTP/2.
	stopReload := make(chan struct{})
	defer close(stopReload)
	if cfg.Master.TLS != nil {
		httpSrv.Handler = rootHandler
		// Serve the cert via a hot-reloading GetCertificate so a renewed wildcard
		// (Let's Encrypt rotates ~every 90 days) is applied without a restart.
		reloader, err := tlsreload.New(cfg.Master.TLS.Cert, cfg.Master.TLS.Key, logger)
		if err != nil {
			return err
		}
		httpSrv.TLSConfig = &tls.Config{
			GetCertificate: reloader.GetCertificate,
			MinVersion:     tls.VersionTLS12,
			NextProtos:     []string{"h2", "http/1.1"}, // h2 for worker streams, http/1.1 for browsers
		}
		go reloader.Watch(stopReload, 15*time.Minute)
	} else {
		logger.Warn("no TLS configured; serving HTTP/2 cleartext (h2c) — use TLS in production")
		httpSrv.Handler = h2c.NewHandler(rootHandler, &http2.Server{})
	}

	// Heartbeat reaper: fail sessions on dead workers.
	reapCtx, cancelReap := context.WithCancel(ctx)
	defer cancelReap()
	go srv.ReapLoop(reapCtx, cfg.Master.WorkerTimeout(), 0)

	// Serve until a signal arrives.
	errCh := make(chan error, 1)
	go func() {
		logger.Info("control plane listening", "addr", cfg.Master.Listen, "tls", cfg.Master.TLS != nil)
		if cfg.Master.TLS != nil {
			// Empty file args: the cert comes from TLSConfig.GetCertificate (the reloader).
			errCh <- httpSrv.ListenAndServeTLS("", "")
		} else {
			errCh <- httpSrv.ListenAndServe()
		}
	}()

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	case <-sigCtx.Done():
		logger.Info("shutdown signal received; draining")
		cancelReap()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		// Best-effort forensic backup beside the live DB (the running txns are the
		// real durability story). Ignore a failure — nothing to act on at exit.
		if err := store.BackupTo(store.Path() + ".bak"); err != nil {
			logger.Warn("control-plane backup on shutdown failed", "err", err)
		}
		eventBus.Close()
		return nil
	}
}

// moveAside renames a corrupt DB file aside so a fresh one can be created in
// its place. Best-effort: if even the rename fails we log and continue — the
// store is already unusable and Open's recreate path in the caller has already
// moved on.
func moveAside(path string, logger *slog.Logger) {
	corrupt := fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix())
	if err := os.Rename(path, corrupt); err != nil {
		logger.Error("failed to set aside corrupt control-plane DB", "err", err, "path", path)
		return
	}
	logger.Warn("set aside corrupt control-plane DB; a fresh one will be created", "corrupt", corrupt)
}
