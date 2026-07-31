//go:build unix

package provider

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Bridge exposes exactly one provider MCP session through a run-private Unix
// socket. The daemon keeps the grant and ToolProxy in the Server; the Codex
// child only receives this socket path through its private configuration.
type Bridge struct {
	listener *net.UnixListener
	server   *Server
	socket   string

	mu     sync.Mutex
	conn   *net.UnixConn
	done   chan struct{}
	stop   sync.Once
	ctx    context.Context
	cancel context.CancelFunc
}

// StartBridge opens one owner-only Unix socket and begins accepting the MCP
// child. It accepts one connection only, so no later process can bind to the
// run's server after the Codex child disconnects.
func StartBridge(socket string, server *Server) (*Bridge, error) {
	if server == nil {
		return nil, errors.New("provider MCP server is required")
	}
	if strings.TrimSpace(socket) == "" || !filepath.IsAbs(socket) {
		return nil, errors.New("absolute provider MCP socket is required")
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Dir(socket), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("provider MCP socket path is not a socket")
		}
		if err := os.Remove(socket); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(socket)
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	bridge := &Bridge{listener: listener, server: server, socket: socket, done: make(chan struct{}), ctx: ctx, cancel: cancel}
	go bridge.serve()
	return bridge, nil
}

func (b *Bridge) serve() {
	defer close(b.done)
	defer os.Remove(b.socket)
	connection, err := b.listener.AcceptUnix()
	if err != nil {
		return
	}
	b.mu.Lock()
	b.conn = connection
	b.mu.Unlock()
	_ = b.listener.Close()
	_ = b.server.Serve(b.ctx, connection, connection)
	_ = connection.Close()
}

// Close interrupts an active MCP exchange and removes the run-private socket.
func (b *Bridge) Close() error {
	b.stop.Do(func() {
		b.cancel()
		_ = b.listener.Close()
		b.mu.Lock()
		connection := b.conn
		b.mu.Unlock()
		if connection != nil {
			_ = connection.Close()
		}
	})
	<-b.done
	return nil
}
