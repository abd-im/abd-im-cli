package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/abd-im/abd-im-cli/internal/bridge"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/profile"
)

var (
	ErrRuntimeStarted = errors.New("daemon runtime already started")
	ErrRuntimeStopped = errors.New("daemon runtime is stopped")
)

// RuntimeConfig contains the already-composed dependencies of one daemon
// profile. The composition root owns SDK connection settings and credentials;
// Runtime only controls their lifecycle and local IPC exposure.
type RuntimeConfig struct {
	UserSDKFactory bridge.SDKFactory
	BotSDKFactory  bridge.SDKFactory
	LockFile       string
	SocketPath     string
	Inbound        *Inbound
	Handler        ipc.Handler
}

// Runtime owns one profile's SDK lifecycle, inbound path, and local socket.
// It starts accepting RPC only after both SDK identities are ready.
type Runtime struct {
	user       *bridge.LoginMgr
	bot        *bridge.LoginMgr
	inbound    *Inbound
	lockPath   string
	socketPath string
	handler    ipc.Handler

	mu      sync.Mutex
	server  *ipc.Server
	lock    *profile.Lock
	state   bridge.State
	started bool
	stopped bool
}

// NewRuntime validates and assembles a daemon runtime without allocating an
// SDK context or listening on the socket.
func NewRuntime(config RuntimeConfig) (*Runtime, error) {
	if config.UserSDKFactory == nil || config.BotSDKFactory == nil || strings.TrimSpace(config.LockFile) == "" || strings.TrimSpace(config.SocketPath) == "" || config.Inbound == nil || config.Handler == nil {
		return nil, errors.New("user SDK, bot SDK, lock file, socket path, inbound path, and RPC handler are required")
	}
	user, err := bridge.NewLoginMgr(config.UserSDKFactory, nil)
	if err != nil {
		return nil, err
	}
	bot, err := bridge.NewLoginMgr(config.BotSDKFactory, config.Inbound.Listener)
	if err != nil {
		return nil, err
	}
	return &Runtime{user: user, bot: bot, inbound: config.Inbound, lockPath: config.LockFile, socketPath: config.SocketPath, handler: config.Handler, state: bridge.StateNew}, nil
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

	lock, err := profile.AcquireLock(r.lockPath)
	if err != nil {
		if errors.Is(err, profile.ErrLocked) {
			r.state = bridge.StateLocked
		} else {
			r.state = bridge.StateDegraded
		}
		return r.failStart(err)
	}
	r.lock = lock
	if err := r.user.Start(ctx); err != nil {
		return r.failStart(fmt.Errorf("start user SDK: %w", err))
	}
	if err := r.bot.Start(ctx); err != nil {
		return r.failStart(fmt.Errorf("start bot SDK: %w", err))
	}
	server, err := ipc.Listen(r.socketPath, r.handler)
	if err != nil {
		return r.failStart(fmt.Errorf("listen on daemon socket: %w", err))
	}
	r.server = server
	r.state = bridge.StateReady
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
// to emit its ready response only after both SDKs and the local socket are live.
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
	lock := r.lock
	r.server = nil
	r.lock = nil
	r.state = bridge.StateStopped
	r.mu.Unlock()

	var result error
	if server != nil {
		result = server.Close()
	}
	if err := r.inbound.Shutdown(ctx); err != nil {
		result = errors.Join(result, fmt.Errorf("shutdown inbound path: %w", err))
	}
	if err := r.bot.Shutdown(ctx); err != nil {
		result = errors.Join(result, fmt.Errorf("shutdown bot SDK: %w", err))
	}
	if err := r.user.Shutdown(ctx); err != nil {
		result = errors.Join(result, fmt.Errorf("shutdown user SDK: %w", err))
	}
	if lock != nil {
		result = errors.Join(result, lock.Release())
	}
	return result
}

// State returns the underlying SDK bridge lifecycle state.
func (r *Runtime) State() bridge.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// failStart runs while Start holds r.mu, so it must not call Shutdown.
func (r *Runtime) failStart(startErr error) error {
	r.stopped = true
	if r.state != bridge.StateLocked {
		r.state = bridge.StateDegraded
	}
	if err := r.inbound.Shutdown(context.Background()); err != nil {
		startErr = errors.Join(startErr, fmt.Errorf("shutdown inbound path: %w", err))
	}
	if err := r.bot.Shutdown(context.Background()); err != nil {
		startErr = errors.Join(startErr, err)
	}
	if err := r.user.Shutdown(context.Background()); err != nil {
		startErr = errors.Join(startErr, err)
	}
	if r.lock != nil {
		startErr = errors.Join(startErr, r.lock.Release())
		r.lock = nil
	}
	return startErr
}
