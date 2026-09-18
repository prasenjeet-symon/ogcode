package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
)

// TestDialStreamHonoursNetwork pins the narrowing rule. Only the unqualified
// "tcp" is rewritten: a caller that asked for tcp6 explicitly must not be
// silently handed an IPv4 socket, because that is a contradiction rather than a
// preference.
func TestDialStreamHonoursNetwork(t *testing.T) {
	// A listener on loopback gives dialStream something real to connect to, so
	// this exercises the actual dial path rather than a stubbed one.
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no IPv4 loopback available: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	conn, err := dialStream(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dialStream over tcp: %v", err)
	}
	defer conn.Close()
	if ip := conn.RemoteAddr().(*net.TCPAddr).IP; ip.To4() == nil {
		t.Errorf("expected an IPv4 peer, got %s", ip)
	}
}

// TestDialStreamRejectsMismatch checks that narrowing to tcp4 cannot reach an
// IPv6-only destination — the failure an operator should see if they pin IPv4
// on a network that has no IPv4 path, rather than a confusing hang.
func TestDialStreamRejectsMismatch(t *testing.T) {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback available: %v", err)
	}
	defer ln.Close()

	if _, err := streamDialer.DialContext(context.Background(), "tcp4", ln.Addr().String()); err == nil {
		t.Error("dialing an IPv6-only listener over tcp4 should fail")
	}
}

// TestParseIPv4Mode covers the three positions. The off spellings matter as
// much as the on ones: they are the only way to keep IPv6 on a host where the
// automatic fallback would otherwise strand provider traffic on a broken IPv4.
func TestParseIPv4Mode(t *testing.T) {
	for _, raw := range []string{"1", "true", "yes", "on", "TRUE", "  On  "} {
		if got := parseIPv4Mode(raw); got != ipv4Always {
			t.Errorf("parseIPv4Mode(%q) = %v, want ipv4Always", raw, got)
		}
	}
	for _, raw := range []string{"0", "false", "no", "off", "never", "OFF"} {
		if got := parseIPv4Mode(raw); got != ipv4Never {
			t.Errorf("parseIPv4Mode(%q) = %v, want ipv4Never", raw, got)
		}
	}
	// Unset is auto. So is anything unrecognised — a typo must not silently
	// disarm a protection the operator was trying to switch on.
	for _, raw := range []string{"", "   ", "maybe", "ipv4"} {
		if got := parseIPv4Mode(raw); got != ipv4Auto {
			t.Errorf("parseIPv4Mode(%q) = %v, want ipv4Auto", raw, got)
		}
	}
}

// TestIsIPv6PathFailure pins what counts as the IPv6 path giving out. The test
// uses the exact addresses from the reported failure: a Jio temporary source
// address that had been rotated away, and openrouter.ai's Cloudflare endpoint.
func TestIsIPv6PathFailure(t *testing.T) {
	v6Remote := &net.TCPAddr{IP: net.ParseIP("2606:4700:8d90:eaa1:32ef:c1c:ba36:2d7e"), Port: 443}
	v4Remote := &net.TCPAddr{IP: net.ParseIP("104.18.2.1"), Port: 443}
	opErr := func(addr net.Addr, errno syscall.Errno) error {
		return &net.OpError{Op: "read", Net: "tcp", Addr: addr, Err: os.NewSyscallError("read", errno)}
	}

	yes := map[string]error{
		"EHOSTUNREACH to an IPv6 peer": opErr(v6Remote, syscall.EHOSTUNREACH),
		"ENETUNREACH to an IPv6 peer":  opErr(v6Remote, syscall.ENETUNREACH),
		"wrapped by the stream layer":  fmt.Errorf("stream read failed: %w", opErr(v6Remote, syscall.EHOSTUNREACH)),
	}
	for name, err := range yes {
		if !isIPv6PathFailure(err) {
			t.Errorf("%s: isIPv6PathFailure = false, want true", name)
		}
	}

	no := map[string]error{
		// The same errno over IPv4 says nothing about IPv6.
		"EHOSTUNREACH to an IPv4 peer": opErr(v4Remote, syscall.EHOSTUNREACH),
		// A peer that resets the connection is not an address-family problem,
		// and counting it would fall back to IPv4 for an unrelated reason.
		"ECONNRESET to an IPv6 peer": opErr(v6Remote, syscall.ECONNRESET),
		"EOF":                        io.ErrUnexpectedEOF,
		"no address at all":          &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.EHOSTUNREACH)},
	}
	for name, err := range no {
		if isIPv6PathFailure(err) {
			t.Errorf("%s: isIPv6PathFailure = true, want false", name)
		}
	}
}

// TestNoteIPv6FailureTripsAfterStrikes walks the fallback through its states:
// one failure is tolerated, the second trips it, and dialing narrows from then on.
func TestNoteIPv6FailureTripsAfterStrikes(t *testing.T) {
	if ipv4Setting() != ipv4Auto {
		t.Skipf("%s is set in this environment", forceIPv4Env)
	}
	if !hasGlobalIPv4() {
		t.Skip("host has no global IPv4 address, so the fallback cannot arm")
	}
	resetIPv6Fallback(t)

	v6 := &net.TCPAddr{IP: net.ParseIP("2606:4700::1"), Port: 443}
	fail := fmt.Errorf("stream read failed: %w",
		&net.OpError{Op: "read", Net: "tcp", Addr: v6, Err: os.NewSyscallError("read", syscall.EHOSTUNREACH)})

	noteIPv6Failure(fail)
	if useIPv4() {
		t.Fatal("a single IPv6 failure should not trip the fallback")
	}
	noteIPv6Failure(fail)
	if !useIPv4() {
		t.Fatal("a second IPv6 failure should trip the fallback")
	}

	// Once tripped it stays tripped, and the dialer acts on it.
	noteIPv6Failure(fail)
	if !useIPv4() {
		t.Error("the fallback must not un-trip")
	}
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	defer ln.Close()
	if _, err := dialStream(context.Background(), "tcp", ln.Addr().String()); err == nil {
		t.Error("after falling back, an IPv6-only destination should be unreachable over tcp")
	}
}

// TestNoteIPv6FailureIgnoresUnrelated checks the counter is not moved by
// failures that say nothing about IPv6 — otherwise ordinary provider flakiness
// would eventually disable IPv6 for no reason.
func TestNoteIPv6FailureIgnoresUnrelated(t *testing.T) {
	if ipv4Setting() != ipv4Auto {
		t.Skipf("%s is set in this environment", forceIPv4Env)
	}
	resetIPv6Fallback(t)

	v4 := &net.TCPAddr{IP: net.ParseIP("104.18.2.1"), Port: 443}
	v6 := &net.TCPAddr{IP: net.ParseIP("2606:4700::1"), Port: 443}
	for _, err := range []error{
		&net.OpError{Op: "read", Net: "tcp", Addr: v4, Err: os.NewSyscallError("read", syscall.EHOSTUNREACH)},
		&net.OpError{Op: "read", Net: "tcp", Addr: v6, Err: os.NewSyscallError("read", syscall.ECONNRESET)},
		io.ErrUnexpectedEOF,
		errors.New("stream read failed: provider closed the connection mid-response"),
	} {
		noteIPv6Failure(err)
		noteIPv6Failure(err)
		noteIPv6Failure(err)
	}
	if useIPv4() {
		t.Error("unrelated failures must not trip the IPv6 fallback")
	}
}

// resetIPv6Fallback puts the process-wide fallback state back to untripped, and
// restores it afterwards so these tests cannot leak into the rest of the package.
func resetIPv6Fallback(t *testing.T) {
	t.Helper()
	strikes, fell := ipv6Strikes.Load(), ipv6FellBack.Load()
	ipv6Strikes.Store(0)
	ipv6FellBack.Store(false)
	t.Cleanup(func() {
		ipv6Strikes.Store(strikes)
		ipv6FellBack.Store(fell)
	})
}
