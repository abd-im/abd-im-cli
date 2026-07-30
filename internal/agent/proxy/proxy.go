// Package proxy exposes only run-scoped typed provider tools.
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/abd-im-cli/abdim-cli/internal/agent/grant"
	"github.com/abd-im-cli/abdim-cli/internal/contracts"
)

var ErrClosed = errors.New("run tool proxy is closed")

// Method is one statically registered typed tool. There is intentionally no
// endpoint, command, SQL, or SDK-function field that a provider could override.
type Method struct {
	Name    string
	Scope   string
	Targets func(json.RawMessage) ([]string, error)
	Handle  func(context.Context, contracts.Request, grant.Grant) (json.RawMessage, error)
}

// Proxy is a private in-memory contract implementation for one run.
type Proxy struct {
	grants    *grant.Store
	runID     string
	profileID string
	methods   map[string]Method

	mu     sync.Mutex
	closed bool
}

func New(grants *grant.Store, runID, profileID string, methods []Method) (*Proxy, error) {
	if grants == nil {
		return nil, errors.New("grant store is required")
	}
	if runID == "" || profileID == "" {
		return nil, errors.New("run ID and profile ID are required")
	}
	registered := make(map[string]Method, len(methods))
	for _, method := range methods {
		if method.Name == "" || method.Scope == "" || method.Handle == nil {
			return nil, errors.New("typed method name, scope, and handler are required")
		}
		if _, exists := registered[method.Name]; exists {
			return nil, fmt.Errorf("duplicate typed method %q", method.Name)
		}
		registered[method.Name] = method
	}
	return &Proxy{grants: grants, runID: runID, profileID: profileID, methods: registered}, nil
}

// Call verifies every request at the restricted boundary before invoking its
// typed handler. The provider cannot reach a daemon socket from this surface.
func (p *Proxy) Call(ctx context.Context, request contracts.Request) (contracts.Response, error) {
	if err := request.Validate(); err != nil {
		return failed(request.RequestID, contracts.CodeInvalidArgument, err.Error()), nil
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return failed(request.RequestID, contracts.CodeGrantInvalid, ErrClosed.Error()), nil
	}
	if request.ProfileID != p.profileID {
		return failed(request.RequestID, contracts.CodeGrantInvalid, "profile does not match run"), nil
	}
	method, exists := p.methods[request.Method]
	if !exists {
		return failed(request.RequestID, contracts.CodePolicyDenied, "method is not an exposed typed tool"), nil
	}
	targets := []string(nil)
	if method.Targets != nil {
		var err error
		targets, err = method.Targets(request.Params)
		if err != nil {
			return failed(request.RequestID, contracts.CodeInvalidArgument, "invalid typed method parameters"), nil
		}
		for _, target := range targets {
			if target == "" {
				return failed(request.RequestID, contracts.CodeInvalidArgument, "typed method target is required"), nil
			}
		}
	}
	access, err := p.grants.Authorize(request.Grant, p.runID, p.profileID, method.Name, method.Scope, targets)
	if err != nil {
		return grantFailure(request.RequestID, err), nil
	}
	payload, err := method.Handle(ctx, request, access)
	if err != nil {
		return failed(request.RequestID, contracts.CodeInternal, "typed tool failed"), nil
	}
	if !json.Valid(payload) {
		return failed(request.RequestID, contracts.CodeInternal, "typed tool returned invalid JSON"), nil
	}
	return contracts.Response{
		APIVersion: contracts.APIVersionV1,
		RequestID:  request.RequestID,
		OK:         true,
		Data:       payload,
		Meta:       &contracts.Meta{ProfileID: p.profileID},
	}, nil
}

// Close invalidates every credential for this run before the provider can make
// another tool call. It does not contact a daemon/controller endpoint.
func (p *Proxy) Close(context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	p.grants.RevokeRun(p.runID)
	return nil
}

func grantFailure(requestID string, err error) contracts.Response {
	code := contracts.CodeGrantInvalid
	if errors.Is(err, grant.ErrMethodDenied) || errors.Is(err, grant.ErrScopeDenied) || errors.Is(err, grant.ErrTargetDenied) || errors.Is(err, grant.ErrRateLimited) {
		code = contracts.CodePolicyDenied
	}
	return failed(requestID, code, "grant does not authorize typed tool call")
}

func failed(requestID string, code contracts.ErrorCode, message string) contracts.Response {
	return contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: requestID, OK: false, Error: &contracts.Error{Code: code, Message: message}}
}

var _ contracts.ToolProxy = (*Proxy)(nil)
