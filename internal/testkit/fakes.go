// Package testkit contains in-memory test doubles for abdim contracts.
package testkit

import (
	"context"
	"errors"
	"sync"

	"github.com/abd-im-cli/abdim-cli/internal/contracts"
)

var (
	ErrListenerUnset = errors.New("fake SDK event listener is not configured")
	ErrProxyClosed   = errors.New("fake tool proxy is closed")
	ErrNoProxyResult = errors.New("fake tool proxy has no configured result")
)

// FakeSDK records lifecycle calls and emits normalized events without an SDK.
type FakeSDK struct {
	mu sync.Mutex

	InitSDKErr       error
	InitResourcesErr error
	SetListenerErr   error
	LoginErr         error
	ShutdownErr      error

	steps    []string
	listener contracts.EventListener
}

func (f *FakeSDK) InitSDK(context.Context) error {
	f.record("InitSDK")
	return f.InitSDKErr
}

func (f *FakeSDK) InitResources(context.Context) error {
	f.record("InitResources")
	return f.InitResourcesErr
}

func (f *FakeSDK) SetEventListener(listener contracts.EventListener) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, "SetEventListener")
	if f.SetListenerErr != nil {
		return f.SetListenerErr
	}
	f.listener = listener
	return nil
}

func (f *FakeSDK) Login(context.Context) error {
	f.record("Login")
	return f.LoginErr
}

func (f *FakeSDK) Shutdown(context.Context) error {
	f.record("Shutdown")
	return f.ShutdownErr
}

func (f *FakeSDK) Emit(ctx context.Context, event contracts.Event) error {
	f.mu.Lock()
	listener := f.listener
	f.mu.Unlock()
	if listener == nil {
		return ErrListenerUnset
	}
	listener(ctx, event)
	return nil
}

func (f *FakeSDK) Steps() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.steps...)
}

func (f *FakeSDK) record(step string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, step)
}

// FakeProvider supplies a preconfigured in-memory Session.
type FakeProvider struct {
	mu sync.Mutex

	StartErr  error
	StartFunc func(context.Context, contracts.StartRequest) (contracts.Session, error)
	Session   *FakeSession
	starts    []contracts.StartRequest
}

func (f *FakeProvider) Start(ctx context.Context, request contracts.StartRequest) (contracts.Session, error) {
	f.mu.Lock()
	f.starts = append(f.starts, request)
	start := f.StartFunc
	err := f.StartErr
	session := f.Session
	if session == nil && start == nil && err == nil {
		session = &FakeSession{}
		f.Session = session
	}
	f.mu.Unlock()

	if start != nil {
		return start(ctx, request)
	}
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (f *FakeProvider) Starts() []contracts.StartRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.StartRequest(nil), f.starts...)
}

// FakeSession records provider turns and lifecycle calls.
type FakeSession struct {
	mu sync.Mutex

	TurnResults []contracts.TurnResult
	TurnErr     error
	CancelErr   error
	CloseErr    error

	turns       []contracts.TurnRequest
	cancelCount int
	closeCount  int
}

func (f *FakeSession) Turn(_ context.Context, request contracts.TurnRequest) (contracts.TurnResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turns = append(f.turns, request)
	if f.TurnErr != nil {
		return contracts.TurnResult{}, f.TurnErr
	}
	if len(f.TurnResults) == 0 {
		return contracts.TurnResult{}, nil
	}
	result := f.TurnResults[0]
	f.TurnResults = f.TurnResults[1:]
	return result, nil
}

func (f *FakeSession) Cancel(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelCount++
	return f.CancelErr
}

func (f *FakeSession) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCount++
	return f.CloseErr
}

func (f *FakeSession) Turns() []contracts.TurnRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.TurnRequest(nil), f.turns...)
}

func (f *FakeSession) CancelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelCount
}

func (f *FakeSession) CloseCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeCount
}

// FakeProxy records tool calls without exposing a daemon connection.
type FakeProxy struct {
	mu sync.Mutex

	CallFunc func(context.Context, contracts.Request) (contracts.Response, error)
	CallErr  error
	Response *contracts.Response
	CloseErr error

	calls      []contracts.Request
	closed     bool
	closeCount int
}

func (f *FakeProxy) Call(ctx context.Context, request contracts.Request) (contracts.Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, request)
	if f.closed {
		f.mu.Unlock()
		return contracts.Response{}, ErrProxyClosed
	}
	call := f.CallFunc
	err := f.CallErr
	var response contracts.Response
	if f.Response != nil {
		response = *f.Response
	}
	hasResponse := f.Response != nil
	f.mu.Unlock()

	if call != nil {
		return call(ctx, request)
	}
	if err != nil {
		return contracts.Response{}, err
	}
	if !hasResponse {
		return contracts.Response{}, ErrNoProxyResult
	}
	return response, nil
}

func (f *FakeProxy) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	f.closeCount++
	return f.CloseErr
}

func (f *FakeProxy) Calls() []contracts.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.Request(nil), f.calls...)
}

func (f *FakeProxy) CloseCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeCount
}

var (
	_ contracts.SDK       = (*FakeSDK)(nil)
	_ contracts.Provider  = (*FakeProvider)(nil)
	_ contracts.Session   = (*FakeSession)(nil)
	_ contracts.ToolProxy = (*FakeProxy)(nil)
)
