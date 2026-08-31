package search

import (
	"bufio"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// browserTransport is an http.RoundTripper whose TLS handshake is a real
// browser's, byte for byte.
//
// This is the single change that matters most for not being blocked, and it is
// worth stating why plainly. Bot detection at Cloudflare, Akamai and the search
// engines themselves does not begin with the User-Agent — it begins with the
// TLS ClientHello, before a single HTTP header has been sent. Go's crypto/tls
// emits a cipher suite list, extension set and extension order that no browser
// produces, so its JA3/JA4 hash is a constant that vendors ship on their
// blocklists. Any amount of header spoofing on top of that is decoration on a
// client that already announced itself in the first packet.
//
// uTLS solves it by emitting a recorded browser ClientHello instead. Paired
// with the matching persona headers, the request stops being distinguishable
// at the layers that are cheap for a defender to check.
//
// What this does not fix: header *order*. net/http sorts headers for HTTP/1.1
// and x/net/http2 sorts them again for HPACK, whereas browsers use a fixed
// non-alphabetical order that is itself fingerprinted. Fixing that means owning
// forks of both packages. The Safari backend is the answer for targets that
// check it — it is an actual browser, so it gets ordering, JavaScript and
// cookies right without any of this machinery.
type browserTransport struct {
	persona persona
	dialer  *net.Dialer

	// Four transports, because the protocol is not knowable until the handshake
	// is done: h2 for the common case, h1 for hosts that turn out not to speak
	// it, plain for http:// (which in practice means tests and local
	// endpoints), and proxied for when the environment names a forward proxy
	// the forged handshake cannot reach through.
	h2      *http2.Transport
	h1      *http.Transport
	plain   *http.Transport
	proxied *http.Transport

	// proxyNoted keeps the "fingerprinting is off behind a proxy" notice to one
	// line per process rather than one per request.
	proxyNoted sync.Once

	// mu guards downgraded, which records the hosts that answered the ALPN
	// offer with http/1.1 so later requests skip straight to h1.
	mu         sync.Mutex
	downgraded map[string]bool
}

// errNotHTTP2 is returned by the h2 dialer when the server declined h2 in ALPN.
// http2.Transport delegates entirely to DialTLSContext and does not check the
// negotiated protocol itself, so the check has to live here.
var errNotHTTP2 = errors.New("search: server did not negotiate http/2")

const (
	// browserDialTimeout bounds the TCP connect and TLS handshake. Separate
	// from the per-request deadline so a black-holed host fails fast rather
	// than consuming the caller's whole budget.
	browserDialTimeout = 12 * time.Second

	// browserIdleTimeout is how long a pooled connection is kept. Browsers
	// keep connections alive across a browsing session; closing after every
	// request and reconnecting is both slower and less browser-like.
	browserIdleTimeout = 90 * time.Second
)

// newBrowserTransport builds a transport that presents the given persona.
func newBrowserTransport(p persona) *browserTransport {
	t := &browserTransport{
		persona:    p,
		dialer:     &net.Dialer{Timeout: browserDialTimeout, KeepAlive: 30 * time.Second},
		downgraded: make(map[string]bool),
	}

	t.h2 = &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return t.dial(ctx, network, addr, true)
		},
		// Without this a connection that dies silently (a dropped NAT mapping
		// mid-research-run is the common case) is only discovered when a
		// request hangs on it.
		ReadIdleTimeout: 30 * time.Second,
		PingTimeout:     10 * time.Second,
	}

	t.h1 = &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return t.dial(ctx, network, addr, false)
		},
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     browserIdleTimeout,
		// The custom dialer already speaks TLS; leaving this on would have
		// net/http try to negotiate h2 over a conn we have pinned to h1.
		ForceAttemptHTTP2: false,
	}

	t.plain = &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         t.dialer.DialContext,
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     browserIdleTimeout,
	}

	// The proxied path deliberately gives up the forged handshake.
	//
	// A forward proxy terminates or tunnels the connection, and reaching one
	// means speaking CONNECT before any TLS happens — the custom dialer here
	// dials the origin directly and would simply not reach it. Being
	// unreachable behind a corporate proxy is a far worse failure than being
	// fingerprintable, and this transport replaced a plain http.Client that
	// honoured the proxy environment by default, so silently dropping that
	// would be a regression rather than a trade.
	t.proxied = &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         t.dialer.DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     browserIdleTimeout,
	}

	return t
}

// RoundTrip sends the request over whichever protocol the host speaks.
//
// Retrying on the h2→h1 downgrade is safe because every request this transport
// carries is a bodyless GET: there is nothing to rewind. Should that ever stop
// being true, the retry has to be gated on req.GetBody being non-nil.
func (t *browserTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return decompressResponse(t.plain.RoundTrip(req))
	}

	if proxy, err := http.ProxyFromEnvironment(req); err == nil && proxy != nil {
		t.proxyNoted.Do(func() {
			slog.Info("web search: a proxy is configured, so requests go through it with Go's own TLS. "+
				"Engines that fingerprint the handshake may refuse these; unset the proxy for this host to "+
				"use the browser-shaped transport.", "proxy", proxy.Host)
		})
		return decompressResponse(t.proxied.RoundTrip(req))
	}

	if t.isDowngraded(req.URL.Host) {
		return decompressResponse(t.h1.RoundTrip(req))
	}

	resp, err := t.h2.RoundTrip(req)
	if err != nil && errors.Is(err, errNotHTTP2) {
		t.markDowngraded(req.URL.Host)
		return decompressResponse(t.h1.RoundTrip(req))
	}
	return decompressResponse(resp, err)
}

func (t *browserTransport) isDowngraded(host string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.downgraded[host]
}

func (t *browserTransport) markDowngraded(host string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.downgraded[host] = true
}

// dial performs the TCP connect and the forged TLS handshake.
//
// requireH2 distinguishes the two callers. The h2 transport offers the
// browser's real ALPN list ("h2", "http/1.1") and rejects the connection if the
// server picks the latter, so the caller can fall back. The h1 transport is
// only ever reached for a host already known not to speak h2, and offers
// http/1.1 alone — a deviation from the recorded ClientHello, but one confined
// to hosts that were going to answer http/1.1 regardless.
func (t *browserTransport) dial(ctx context.Context, network, addr string, requireH2 bool) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	raw, err := t.dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	cfg := &utls.Config{ServerName: host}
	var conn *utls.UConn
	if requireH2 {
		conn = utls.UClient(raw, cfg, t.persona.hello)
	} else {
		spec, err := http11Spec(t.persona.hello)
		if err != nil {
			raw.Close()
			return nil, err
		}
		conn = utls.UClient(raw, cfg, utls.HelloCustom)
		if err := conn.ApplyPreset(spec); err != nil {
			raw.Close()
			return nil, fmt.Errorf("search: apply tls preset: %w", err)
		}
	}

	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}

	if requireH2 && conn.ConnectionState().NegotiatedProtocol != http2.NextProtoTLS {
		conn.Close()
		return nil, errNotHTTP2
	}
	return conn, nil
}

// http11Spec is the persona's ClientHello with the ALPN offer narrowed to
// http/1.1.
func http11Spec(id utls.ClientHelloID) (*utls.ClientHelloSpec, error) {
	spec, err := utls.UTLSIdToSpec(id)
	if err != nil {
		return nil, fmt.Errorf("search: tls spec for %s: %w", id.Str(), err)
	}
	for _, ext := range spec.Extensions {
		if alpn, ok := ext.(*utls.ALPNExtension); ok {
			alpn.AlpnProtocols = []string{"http/1.1"}
		}
	}
	return &spec, nil
}

// ── content decoding ────────────────────────────────────────────────────────

// decompressResponse undoes the Content-Encoding the persona asked for.
//
// net/http decompresses gzip transparently, but only when it was the one that
// added the Accept-Encoding header. A persona has to send the browser's full
// list — a client advertising only gzip in 2026 is itself unusual — and setting
// the header by hand turns the automatic path off, so decoding becomes ours.
//
// Decoding is deferred to the first Read. Building the decoder here would mean
// reading the stream's header eagerly, which fails on a legitimately empty body
// (a 204, or a 200 with nothing in it) and would turn those into transport
// errors.
func decompressResponse(resp *http.Response, err error) (*http.Response, error) {
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	if encoding == "" || encoding == "identity" {
		return resp, nil
	}

	resp.Body = &lazyDecoder{source: resp.Body, encoding: encoding}
	// The body no longer matches these, and a stale Content-Length is worse
	// than none: callers size buffers from it.
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")
	resp.ContentLength = -1
	resp.Uncompressed = true
	return resp, nil
}

// lazyDecoder wraps a compressed body and builds the real decoder on first use.
type lazyDecoder struct {
	source   io.ReadCloser
	encoding string

	decoder io.Reader
	// closer is set when the decoder holds resources of its own (zstd).
	closer io.Closer
	err    error
	once   sync.Once
}

func (d *lazyDecoder) Read(p []byte) (int, error) {
	d.once.Do(d.init)
	if d.err != nil {
		return 0, d.err
	}
	return d.decoder.Read(p)
}

func (d *lazyDecoder) init() {
	switch d.encoding {
	case "gzip", "x-gzip":
		r, err := gzip.NewReader(d.source)
		if err != nil {
			d.err = fmt.Errorf("search: gzip body: %w", err)
			return
		}
		d.decoder, d.closer = r, r
	case "br":
		d.decoder = brotli.NewReader(d.source)
	case "zstd":
		r, err := zstd.NewReader(d.source)
		if err != nil {
			d.err = fmt.Errorf("search: zstd body: %w", err)
			return
		}
		d.decoder, d.closer = r.IOReadCloser(), r.IOReadCloser()
	case "deflate":
		d.decoder, d.err = inflate(d.source)
	default:
		// An encoding we never asked for. Passing the bytes through would hand
		// the parser plausible-looking garbage, which is worse than failing.
		d.err = fmt.Errorf("search: unsupported content-encoding %q", d.encoding)
	}
}

func (d *lazyDecoder) Close() error {
	if d.closer != nil {
		_ = d.closer.Close()
	}
	return d.source.Close()
}

// inflate handles the two things servers mean by "deflate": the zlib-wrapped
// stream the RFC specifies, and the bare deflate stream some servers send
// anyway. They are told apart by the zlib header, which is why the reader is
// buffered — the bytes have to be peeked before being consumed.
func inflate(r io.Reader) (io.Reader, error) {
	br := bufio.NewReader(r)
	header, err := br.Peek(2)
	if err != nil {
		// Too short to be either; let the decoder report it.
		return flate.NewReader(br), nil
	}
	// zlib: low nibble of byte 0 is 8 (deflate), and the two bytes form a
	// multiple of 31.
	if header[0]&0x0f == 8 && (uint16(header[0])<<8|uint16(header[1]))%31 == 0 {
		zr, err := zlib.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("search: deflate body: %w", err)
		}
		return zr, nil
	}
	return flate.NewReader(br), nil
}
