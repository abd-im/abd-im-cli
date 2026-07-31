// Package bridge owns the lifecycle boundary around an SDK LoginMgr instance.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/profile"
)

// State describes the daemon's SDK lifecycle state.
type State string

const (
	StateNew      State = "new"
	StateStarting State = "starting"
	StateReady    State = "ready"
	StateDegraded State = "degraded"
	StateLocked   State = "locked"
	StateStopped  State = "stopped"
)

var ErrAlreadyStarted = errors.New("login manager already started")

// SDKFactory creates an isolated SDK user context. Its implementation binds
// the SDK's NewLoginMgr constructor without exposing a package-level facade.
type SDKFactory func() contracts.SDK

// LoginMgr is the daemon-owned lifecycle bridge for exactly one profile.
type LoginMgr struct {
	factory      SDKFactory
	lockPath     string
	eventHandler contracts.EventListener

	mu    sync.RWMutex
	state State
	err   error
	sdk   contracts.SDK
	lock  *profile.Lock
}

// NewLoginMgr creates a bridge. It does not allocate an SDK context or acquire
// the profile lock until Start is called.
func NewLoginMgr(factory SDKFactory, lockPath string, eventHandler contracts.EventListener) (*LoginMgr, error) {
	if factory == nil {
		return nil, errors.New("SDK factory is required")
	}
	if lockPath == "" {
		return nil, errors.New("profile lock path is required")
	}
	return &LoginMgr{factory: factory, lockPath: lockPath, eventHandler: eventHandler, state: StateNew}, nil
}

// Start acquires exclusive profile ownership and initializes the SDK in the
// prescribed order. A failure leaves no partially-ready bridge behind.
func (m *LoginMgr) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.state != StateNew {
		m.mu.Unlock()
		return ErrAlreadyStarted
	}
	m.state = StateStarting
	m.mu.Unlock()

	lock, err := profile.AcquireLock(m.lockPath)
	if err != nil {
		if errors.Is(err, profile.ErrLocked) {
			m.fail(StateLocked, err)
			return err
		}
		m.fail(StateDegraded, err)
		return err
	}

	sdk := m.factory()
	if sdk == nil {
		_ = lock.Release()
		err := errors.New("SDK factory returned nil")
		m.fail(StateDegraded, err)
		return err
	}

	m.mu.Lock()
	m.lock = lock
	m.sdk = sdk
	m.mu.Unlock()

	for _, step := range []struct {
		name string
		do   func() error
	}{
		{name: "InitSDK", do: func() error { return sdk.InitSDK(ctx) }},
		{name: "InitResources", do: func() error { return sdk.InitResources(ctx) }},
		{name: "SetEventListener", do: func() error { return sdk.SetEventListener(m.handleEvent) }},
		{name: "Login", do: func() error { return sdk.Login(ctx) }},
	} {
		if err := step.do(); err != nil {
			m.failAndRelease(ctx, fmt.Errorf("%s: %w", step.name, err))
			return m.Err()
		}
	}

	m.mu.Lock()
	m.state = StateReady
	m.err = nil
	m.mu.Unlock()
	return nil
}

// State returns the current lifecycle state.
func (m *LoginMgr) State() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// Err returns the reason the bridge is degraded or locked.
func (m *LoginMgr) Err() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.err
}

// Shutdown stops event delivery, shuts down the SDK, and releases exclusive
// profile ownership. It is safe after a failed or repeated Start attempt.
func (m *LoginMgr) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.state == StateStopped {
		m.mu.Unlock()
		return nil
	}
	sdk := m.sdk
	lock := m.lock
	m.sdk = nil
	m.lock = nil
	m.state = StateStopped
	m.mu.Unlock()

	var result error
	if sdk != nil {
		if err := sdk.Shutdown(ctx); err != nil {
			result = fmt.Errorf("shutdown SDK: %w", err)
		}
	}
	if lock != nil {
		if err := lock.Release(); err != nil && result == nil {
			result = fmt.Errorf("release profile lock: %w", err)
		}
	}
	return result
}

func (m *LoginMgr) handleEvent(ctx context.Context, event contracts.SDKEvent) {
	m.mu.RLock()
	ready := m.state == StateReady
	handler := m.eventHandler
	m.mu.RUnlock()
	if ready && handler != nil {
		handler(ctx, event)
	}
}

func (m *LoginMgr) fail(state State, err error) {
	m.mu.Lock()
	m.state = state
	m.err = err
	m.mu.Unlock()
}

func (m *LoginMgr) failAndRelease(ctx context.Context, err error) {
	m.mu.Lock()
	sdk := m.sdk
	lock := m.lock
	m.lock = nil
	m.sdk = nil
	m.state = StateDegraded
	m.err = err
	m.mu.Unlock()
	if sdk != nil {
		_ = sdk.Shutdown(ctx)
	}
	if lock != nil {
		_ = lock.Release()
	}
}
