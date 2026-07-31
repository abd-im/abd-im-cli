package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/abd-im/abd-im-cli/internal/bridge"
	"github.com/abd-im/abd-im-cli/internal/ipc"
)

var (
	ErrRuntimeStarted = errors.New("daemon runtime already started")
	ErrRuntimeStopped = errors.New("daemon runtime is stopped")
)

// RuntimeConfig contains the already-composed dependencies of one daemon
// profile. The composition root owns SDK connection settings and credentials;
// Runtime only controls their lifecycle and local IPC exposure.
type RuntimeConfig struct {
	SDKFactory bridge.SDKFactory
	LockFile   string
	SocketPath string
	Inbound    *Inbound
	Handler    ipc.Handler
}

// Runtime owns one profile's SDK lifecycle, inbound path, and owner-only
// local socket. It starts accepting RPC only after the SDK bridge is ready.
type Runtime struct {
	manager    *bridge.LoginMgr
	inbound    *Inbound
	socketPath string
	handler    ipc.Handler

	mu      sync.Mutex
	server  *ipc.Server
	started bool
	stopped bool
}

// NewRuntime validates and assembles a daemon runtime without allocating an
// SDK context or listening on the socket.
func NewRuntime(config RuntimeConfig) (*Runtime, error) {
	if config.SDKFactory == nil || strings.TrimSpace(config.LockFile) == "" || strings.TrimSpace(config.SocketPath) == "" || config.Inbound == nil || config.Handler == nil {
		return nil, errors.New("SDK factory, lock file, socket path, inbound path, and RPC handler are required")
	}
	manager, err := bridge.NewLoginMgr(config.SDKFactory, config.LockFile, config.Inbound.Listener)
	if err != nil {
		return nil, err
	}
	return &Runtime{manager: manager, inbound: config.Inbound, socketPath: config.SocketPath, handler: config.Handler}, nil
}

// Start initializes the SDK first and opens no local RPC listener until the
// bridge reports ready. A failed start leaves no socket accepting requests.
func (r *Runtime) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return ErrRuntimeStopped
	}
	if r.started {
		return ErrRuntimeStarted
	}
	r.started = true

	if err := r.manager.Start(ctx); err != nil {
		return r.failStart(err)
	}
	server, err := ipc.Listen(r.socketPath, r.handler)
	if err != nil {
		return r.failStart(fmt.Errorf("listen on daemon socket: %w", err))
	}
	r.server = server
	return nil
}

// Serve starts the runtime and blocks until the socket closes or ctx is
// canceled. It always releases local SDK ownership before returning.
func (r *Runtime) Serve(ctx context.Context) error {
	if err := r.Start(ctx); err != nil {
		_ = r.Shutdown(context.Background())
		return err
	}
	return r.Wait(ctx)
}

// Wait serves a runtime that has already been started. It is used by the CLI
// to emit its ready response only after the SDK and owner socket are live.
func (r *Runtime) Wait(ctx context.Context) error {
	r.mu.Lock()
	server := r.server
	r.mu.Unlock()
	defer r.Shutdown(context.Background())
	if server == nil {
		return nil
	}
	return server.Serve(ctx)
}

// Shutdown stops local requests, cancels inbound work, then releases the SDK
// and profile lock. It is safe to call before or after Serve returns.
func (r *Runtime) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("shutdown context is required")
	}
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return nil
	}
	r.stopped = true
	server := r.server
	r.server = nil
	r.mu.Unlock()

	var result error
	if server != nil {
		result = server.Close()
	}
	if err := r.inbound.Shutdown(ctx); err != nil {
		result = errors.Join(result, fmt.Errorf("shutdown inbound path: %w", err))
	}
	if err := r.manager.Shutdown(ctx); err != nil {
		result = errors.Join(result, err)
	}
	return result
}

// State returns the underlying SDK bridge lifecycle state.
func (r *Runtime) State() bridge.State { return r.manager.State() }

// failStart runs while Start holds r.mu, so it must not call Shutdown.
func (r *Runtime) failStart(startErr error) error {
	r.stopped = true
	if err := r.inbound.Shutdown(context.Background()); err != nil {
		startErr = errors.Join(startErr, fmt.Errorf("shutdown inbound path: %w", err))
	}
	if err := r.manager.Shutdown(context.Background()); err != nil {
		startErr = errors.Join(startErr, err)
	}
	return startErr
}
