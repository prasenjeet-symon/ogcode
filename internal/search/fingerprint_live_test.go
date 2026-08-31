package search

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// These check the thing the transport exists for: that what leaves this process
// looks like a browser at the TLS and HTTP/2 layers. They need the network and
// a third-party echo service, so they run under the same opt-in as the other
// live tests.
//
//	OGCODE_LIVE_SEARCH_TEST=1 go test ./internal/search/ -run Fingerprint -v

// tlsEchoURL reflects the caller's TLS and HTTP/2 fingerprints back as JSON.
const tlsEchoURL = "https://tls.peet.ws/api/all"

type tlsEcho struct {
	TLS struct {
		JA3     string `json:"ja3"`
		JA3Hash string `json:"ja3_hash"`
		JA4     string `json:"ja4"`
	} `json:"tls"`
	HTTP2 struct {
		AkamaiFingerprint string `json:"akamai_fingerprint"`
	} `json:"http2"`
	HTTPVersion string `json:"http_version"`
	UserAgent   string `json:"user_agent"`
}

func fetchEcho(t *testing.T, b *NativeBackend) tlsEcho {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	body, _, _, err := b.get(ctx, tlsEchoURL, 20*time.Second, navigation{})
	if err != nil {
		t.Skipf("fingerprint echo service unavailable: %v", err)
	}
	var echo tlsEcho
	if err := json.Unmarshal(body, &echo); err != nil {
		t.Skipf("fingerprint echo service returned something unexpected: %v", err)
	}
	return echo
}

// The headline assertion. Go's crypto/tls produces one well-known JA3 whatever
// the User-Agent says, and that is precisely what search engines block. If this
// starts failing, the uTLS transport has stopped being used.
func TestLiveFingerprintIsNotGo(t *testing.T) {
	requireLive(t)

	b := NewNativeBackend()
	echo := fetchEcho(t, b)

	t.Logf("persona   %s", b.persona.name)
	t.Logf("http      %s", echo.HTTPVersion)
	t.Logf("ja3       %s", echo.TLS.JA3Hash)
	t.Logf("ja4       %s", echo.TLS.JA4)
	t.Logf("akamai h2 %s", echo.HTTP2.AkamaiFingerprint)

	if echo.TLS.JA3 == "" {
		t.Fatal("echo service reported no JA3 at all")
	}

	// JA4's first field encodes the transport, ALPN and the counts of cipher
	// suites and extensions. Go's default client offers a markedly smaller set
	// than any browser, so a low count here means the forged ClientHello is not
	// the one going out.
	//
	// The check is on the shape rather than on an exact hash: uTLS bumps its
	// Chrome spec between releases, and pinning a hash would turn a routine
	// dependency upgrade into a failing test for no gain.
	if fields := strings.Split(echo.TLS.JA4, "_"); len(fields) > 0 {
		if strings.HasPrefix(fields[0], "t13d") && len(fields[0]) >= 8 {
			counts := fields[0][4:8] // NNEE: cipher count, extension count
			if counts < "1000" {
				t.Errorf("JA4 %q advertises very few ciphers/extensions — this looks like crypto/tls, not a browser", echo.TLS.JA4)
			}
		}
	}
}

// The persona is only coherent if the User-Agent the server sees is the one
// that belongs to the handshake it saw. This catches the transport and the
// header layer drifting apart.
func TestLiveFingerprintMatchesPersonaUserAgent(t *testing.T) {
	requireLive(t)

	b := NewNativeBackend()
	echo := fetchEcho(t, b)
	if echo.UserAgent != b.persona.userAgent {
		t.Errorf("server saw User-Agent %q, persona sends %q", echo.UserAgent, b.persona.userAgent)
	}
}

// Browsers speak HTTP/2 to anything that offers it. Falling back to HTTP/1.1
// against a host that supports h2 would mean the h2 dialer is rejecting its own
// connections, which costs a handshake per request and is itself unusual
// traffic.
func TestLiveFingerprintNegotiatesHTTP2(t *testing.T) {
	requireLive(t)

	echo := fetchEcho(t, NewNativeBackend())
	if !strings.Contains(echo.HTTPVersion, "2") {
		t.Errorf("negotiated %q, want HTTP/2", echo.HTTPVersion)
	}
}

// Every persona has to work, not just whichever one the pool happened to pick.
// A spec uTLS cannot build, or a header set a server rejects, would otherwise
// only show up for one user in four.
func TestLiveFingerprintAllPersonas(t *testing.T) {
	requireLive(t)

	for _, p := range personas {
		t.Run(p.name, func(t *testing.T) {
			b := NewNativeBackend()
			b.persona = p
			b.http.Transport = newBrowserTransport(p)

			echo := fetchEcho(t, b)
			t.Logf("http=%s ja4=%s", echo.HTTPVersion, echo.TLS.JA4)
			if echo.UserAgent != p.userAgent {
				t.Errorf("server saw %q, want %q", echo.UserAgent, p.userAgent)
			}
			if echo.TLS.JA3 == "" {
				t.Error("no JA3 reported")
			}
		})
	}
}
