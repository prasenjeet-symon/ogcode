package tlsreload

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCert generates a self-signed cert/key with the given serial (so two calls
// produce distinguishable certs) and writes them to certPath/keyPath.
func writeCert(t *testing.T, certPath, keyPath string, serial int64) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return der
}

func currentDER(t *testing.T, r *Reloader) []byte {
	t.Helper()
	c, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	return c.Certificate[0]
}

func TestNewMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(filepath.Join(dir, "nope.crt"), filepath.Join(dir, "nope.key"), nil); err == nil {
		t.Fatal("expected error for missing cert files")
	}
}

func TestReloadPicksUpNewCert(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")

	der1 := writeCert(t, certPath, keyPath, 1)
	r, err := New(certPath, keyPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(currentDER(t, r), der1) {
		t.Fatal("initial cert mismatch")
	}

	// Replace with a different cert and bump modtime into the future so the
	// change is detected regardless of filesystem timestamp granularity.
	der2 := writeCert(t, certPath, keyPath, 2)
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(certPath, future, future); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(keyPath, future, future); err != nil {
		t.Fatal(err)
	}

	changed, err := r.reload(false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("reload did not detect the changed cert")
	}
	if !bytes.Equal(currentDER(t, r), der2) {
		t.Fatal("cert was not swapped to the new one")
	}
}

func TestReloadKeepsOldCertOnError(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	der1 := writeCert(t, certPath, keyPath, 1)
	r, err := New(certPath, keyPath, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the cert file and bump modtime; reload must fail and keep der1.
	if err := os.WriteFile(certPath, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Minute)
	_ = os.Chtimes(certPath, future, future)

	if _, err := r.reload(false); err == nil {
		t.Fatal("expected reload error on corrupt cert")
	}
	if !bytes.Equal(currentDER(t, r), der1) {
		t.Fatal("a failed reload must keep the previous cert")
	}
}
