// Package tunnel adapts a ConnectRPC bidi byte-stream (the Tunnel RPC) into a
// net.Conn and runs a yamux multiplexer over it, so the master can reach a
// worker's loopback-only ogcode web UI. It is an EXPORTED package (not
// internal/) precisely so the ogcode worker in the other repo can import the
// exact same glue — the transport contract must not be duplicated.
//
// Roles: the master is the yamux *client* (it Open()s one stream per incoming
// browser connection and reverse-proxies HTTP over it); the worker is the yamux
// *server* (it Accept()s streams and splices each to its local ogcode). Who
// dialed the underlying RPC is irrelevant to these roles.
package tunnel

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
)

// ChunkStream is the subset of a Connect bidi stream this package needs. Both
// *connect.BidiStream[TunnelChunk,TunnelChunk] (server side) and
// *connect.BidiStreamForClient[TunnelChunk,TunnelChunk] (client side) satisfy it.
type ChunkStream interface {
	Send(*cpv1.TunnelChunk) error
	Receive() (*cpv1.TunnelChunk, error)
}

// conn wraps a ChunkStream as a net.Conn. Reads pull TunnelChunks (buffering the
// remainder); writes send one TunnelChunk per Write. Send is serialized because
// Connect forbids concurrent Send on one stream; a single reader (yamux's recv
// loop) means Read needs no lock.
type conn struct {
	s       ChunkStream
	readBuf []byte
	writeMu sync.Mutex
	closeMu sync.Once
	onClose func()
}

// NewConn adapts a ChunkStream to net.Conn. onClose (may be nil) runs once on
// Close — used by the client side to close the request/response halves.
func NewConn(s ChunkStream, onClose func()) net.Conn {
	return &conn{s: s, onClose: onClose}
}

func (c *conn) Read(p []byte) (int, error) {
	for len(c.readBuf) == 0 {
		chunk, err := c.s.Receive()
		if err != nil {
			if err == io.EOF {
				return 0, io.EOF
			}
			return 0, err
		}
		c.readBuf = chunk.GetData()
	}
	n := copy(p, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}

func (c *conn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	// Copy: the caller may reuse p after Write returns, and the proto value can
	// outlive this call inside the Connect send path.
	data := make([]byte, len(p))
	copy(data, p)
	if err := c.s.Send(&cpv1.TunnelChunk{Data: data}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *conn) Close() error {
	c.closeMu.Do(func() {
		if c.onClose != nil {
			c.onClose()
		}
	})
	return nil
}

type addr struct{}

func (addr) Network() string { return "ogcode-tunnel" }
func (addr) String() string  { return "ogcode-tunnel" }

func (c *conn) LocalAddr() net.Addr  { return addr{} }
func (c *conn) RemoteAddr() net.Addr { return addr{} }

// Deadlines are no-ops: this net.Conn rides a Connect bidi stream that has no
// deadline primitive. yamux does not need them — its stream deadlines are
// enforced internally with timers, and keepalive / write-timeout use time.After
// (verified against yamux v0.1.2), never conn.SetDeadline.
func (c *conn) SetDeadline(time.Time) error      { return nil }
func (c *conn) SetReadDeadline(time.Time) error  { return nil }
func (c *conn) SetWriteDeadline(time.Time) error { return nil }

// config returns the shared yamux settings for both ends. Keepalive is ON so the
// tunnel stays warm: an idle tunnel (no browser using that worker) would
// otherwise be dropped by a proxy/load-balancer idle timeout, and the periodic
// ping also detects a dead peer promptly. The interval is well under a typical
// 60s LB idle timeout.
func config() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 20 * time.Second
	cfg.ConnectionWriteTimeout = 10 * time.Second
	cfg.LogOutput = io.Discard
	return cfg
}

// ClientSession runs the master end (opens streams) over a ChunkStream.
func ClientSession(s ChunkStream, onClose func()) (*yamux.Session, error) {
	return yamux.Client(NewConn(s, onClose), config())
}

// ServerSession runs the worker end (accepts streams) over a ChunkStream.
func ServerSession(s ChunkStream, onClose func()) (*yamux.Session, error) {
	return yamux.Server(NewConn(s, onClose), config())
}
