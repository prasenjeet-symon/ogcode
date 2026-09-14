// Package tlsreload serves a TLS certificate that can be swapped on disk without
// restarting the server. Wildcard certs from Let's Encrypt (via DNS-01) rotate
// every ~90 days; wiring GetCertificate to a Reloader means a renewal is picked
// up in place rather than requiring a restart.
package tlsreload

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Reloader holds the current certificate and re-reads it from disk on demand.
type Reloader struct {
	certPath string
	keyPath  string
	logger   *slog.Logger

	mu   sync.RWMutex
	cert *tls.Certificate
	// modTimes of the files at the last successful load, so a periodic check is
	// cheap and only re-parses when something actually changed.
	certMod time.Time
	keyMod  time.Time
}

// New loads the initial certificate; it errors if the files are missing or the
// pair is invalid, so a misconfigured server fails fast at startup.
func New(certPath, keyPath string, logger *slog.Logger) (*Reloader, error) {
	if logger == nil {
		logger = slog.Default()
	}
	r := &Reloader{certPath: certPath, keyPath: keyPath, logger: logger}
	if _, err := r.reload(true); err != nil {
		return nil, err
	}
	return r, nil
}

// GetCertificate is wired into tls.Config; it returns the currently loaded cert.
func (r *Reloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cert, nil
}

// reload re-reads the pair when the files have changed (or force is set),
// swapping the cached cert on success and keeping the old one on failure.
func (r *Reloader) reload(force bool) (bool, error) {
	certInfo, err := os.Stat(r.certPath)
	if err != nil {
		return false, fmt.Errorf("stat cert %s: %w", r.certPath, err)
	}
	keyInfo, err := os.Stat(r.keyPath)
	if err != nil {
		return false, fmt.Errorf("stat key %s: %w", r.keyPath, err)
	}
	if !force && certInfo.ModTime().Equal(r.certMod) && keyInfo.ModTime().Equal(r.keyMod) {
		return false, nil // unchanged
	}
	cert, err := tls.LoadX509KeyPair(r.certPath, r.keyPath)
	if err != nil {
		return false, fmt.Errorf("load key pair: %w", err)
	}
	r.mu.Lock()
	r.cert = &cert
	r.certMod = certInfo.ModTime()
	r.keyMod = keyInfo.ModTime()
	r.mu.Unlock()
	return true, nil
}

// Watch reloads on SIGHUP and on a periodic timer until stopCh is closed. A
// changed cert is applied to new TLS handshakes immediately; existing
// connections keep the cert they negotiated.
func (r *Reloader) Watch(stopCh <-chan struct{}, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	defer signal.Stop(sighup)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-sighup:
			r.tryReload("SIGHUP")
		case <-ticker.C:
			r.tryReload("periodic")
		}
	}
}

func (r *Reloader) tryReload(reason string) {
	changed, err := r.reload(false)
	switch {
	case err != nil:
		r.logger.Warn("tls cert reload failed; keeping current cert", "reason", reason, "err", err)
	case changed:
		r.logger.Info("tls cert reloaded", "reason", reason, "cert", r.certPath)
	}
}
