// Package profile implements typed profile, user, daemon and doctor reads.
package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/service"
)

const (
	ProfileGet = "profile.get"
	UserMe     = "user.me"
	UserGet    = "user.get"
	DaemonGet  = "daemon.status"
	DoctorGet  = "doctor.get"
)

type Profile struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	CredentialRef string    `json:"credential_ref,omitempty"`
	State         string    `json:"state,omitempty"`
	SDKVersion    string    `json:"sdk_version,omitempty"`
	ServerVersion string    `json:"server_version,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

type User struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Nickname string `json:"nickname,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
	Status   string `json:"status,omitempty"`
}

type DaemonStatus struct {
	ProfileID        string    `json:"profile_id"`
	State            string    `json:"state"`
	PID              int       `json:"pid,omitempty"`
	SDKVersion       string    `json:"sdk_version,omitempty"`
	ServerVersion    string    `json:"server_version,omitempty"`
	LastConnectedAt  time.Time `json:"last_connected_at,omitempty"`
	PendingEvents    int       `json:"pending_events,omitempty"`
	CredentialsValid bool      `json:"credentials_valid"`
}

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type DoctorReport struct {
	OK      bool    `json:"ok"`
	Checks  []Check `json:"checks"`
	Summary string  `json:"summary,omitempty"`
}

// Source is the daemon-owned SDK facade. Implementations may use SDK public
// APIs or a callback bridge, but must not expose the SDK's database here.
type Source interface {
	Profile(context.Context) (Profile, error)
	Self(context.Context) (User, error)
	Users(context.Context, []string) ([]User, error)
	Daemon(context.Context) (DaemonStatus, error)
	Doctor(context.Context) (DoctorReport, error)
}

type Options struct {
	ProfileID    string
	Stale        func() bool
	Capabilities map[string]service.Capability
}

type Service struct {
	source  Source
	options Options
}

func New(source Source, options Options) (*Service, error) {
	if source == nil {
		return nil, errors.New("profile source is required")
	}
	if strings.TrimSpace(options.ProfileID) == "" {
		return nil, errors.New("profile ID is required")
	}
	if options.Stale == nil {
		options.Stale = func() bool { return false }
	}
	if options.Capabilities == nil {
		options.Capabilities = make(map[string]service.Capability)
	}
	return &Service{source: source, options: options}, nil
}

func (s *Service) capability(method string) service.Capability {
	if item, ok := s.options.Capabilities[method]; ok {
		return item
	}
	return service.Capability{Method: method, Scope: method + ".read", Status: "not_validated", Reason: "method has no verified capability entry"}
}

func (s *Service) meta(access service.Access, method string) (service.Meta, error) {
	capability := s.capability(method)
	if capability.Status != "available" {
		return service.Meta{}, fmt.Errorf("%w: %s", service.ErrCapabilityUnavailable, capability.Status)
	}
	if err := access.Authorize(method, capability.Scope); err != nil {
		return service.Meta{}, err
	}
	return service.NewMeta(s.options.ProfileID, s.options.Stale(), capability), nil
}

func (s *Service) Profile(ctx context.Context, access service.Access) (service.Result[Profile], error) {
	meta, err := s.meta(access, ProfileGet)
	if err != nil {
		return service.Result[Profile]{}, err
	}
	item, err := s.source.Profile(ctx)
	if err != nil {
		return service.Result[Profile]{}, fmt.Errorf("read profile: %w", err)
	}
	if item.ID == "" {
		item.ID = s.options.ProfileID
	} else if item.ID != s.options.ProfileID {
		return service.Result[Profile]{}, fmt.Errorf("%w: source returned a different profile", service.ErrTargetDenied)
	}
	return service.Result[Profile]{Data: item, Meta: meta}, nil
}

func (s *Service) Self(ctx context.Context, access service.Access) (service.Result[User], error) {
	meta, err := s.meta(access, UserMe)
	if err != nil {
		return service.Result[User]{}, err
	}
	item, err := s.source.Self(ctx)
	if err != nil {
		return service.Result[User]{}, fmt.Errorf("read self user: %w", err)
	}
	return service.Result[User]{Data: item, Meta: meta}, nil
}

func (s *Service) Users(ctx context.Context, access service.Access, ids []string) (service.Result[[]User], error) {
	if len(ids) == 0 || len(ids) > 100 {
		return service.Result[[]User]{}, fmt.Errorf("%w: user IDs must contain 1-100 items", service.ErrInvalidArgument)
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return service.Result[[]User]{}, fmt.Errorf("%w: user ID is required", service.ErrInvalidArgument)
		}
	}
	capability := s.capability(UserGet)
	if capability.Status != "available" {
		return service.Result[[]User]{}, fmt.Errorf("%w: %s", service.ErrCapabilityUnavailable, capability.Status)
	}
	if err := access.Authorize(UserGet, capability.Scope, ids...); err != nil {
		return service.Result[[]User]{}, err
	}
	items, err := s.source.Users(ctx, append([]string(nil), ids...))
	if err != nil {
		return service.Result[[]User]{}, fmt.Errorf("read users: %w", err)
	}
	allowed := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		allowed[id] = struct{}{}
	}
	for _, item := range items {
		if _, ok := allowed[item.ID]; !ok {
			return service.Result[[]User]{}, fmt.Errorf("%w: source returned an unrequested user", service.ErrTargetDenied)
		}
	}
	return service.Result[[]User]{Data: items, Meta: service.NewMeta(s.options.ProfileID, s.options.Stale(), capability)}, nil
}

func (s *Service) Daemon(ctx context.Context, access service.Access) (service.Result[DaemonStatus], error) {
	meta, err := s.meta(access, DaemonGet)
	if err != nil {
		return service.Result[DaemonStatus]{}, err
	}
	item, err := s.source.Daemon(ctx)
	if err != nil {
		return service.Result[DaemonStatus]{}, fmt.Errorf("read daemon status: %w", err)
	}
	return service.Result[DaemonStatus]{Data: item, Meta: meta}, nil
}

func (s *Service) Doctor(ctx context.Context, access service.Access) (service.Result[DoctorReport], error) {
	meta, err := s.meta(access, DoctorGet)
	if err != nil {
		return service.Result[DoctorReport]{}, err
	}
	item, err := s.source.Doctor(ctx)
	if err != nil {
		return service.Result[DoctorReport]{}, fmt.Errorf("run doctor: %w", err)
	}
	return service.Result[DoctorReport]{Data: item, Meta: meta}, nil
}

// Methods adapts the typed reads to the existing run-scoped tool proxy.
func (s *Service) Methods() []proxy.Method {
	method := func(name, scope string, handle func(context.Context, contracts.Request, service.Access) (interface{}, error)) proxy.Method {
		return proxy.Method{
			Name: name, Scope: scope,
			Meta: func() contracts.Meta {
				return service.ContractMeta(service.NewMeta(s.options.ProfileID, s.options.Stale(), s.capability(name)))
			},
			Targets: func(raw json.RawMessage) ([]string, error) {
				if name != UserGet {
					return nil, nil
				}
				var input struct {
					UserIDs []string `json:"user_ids"`
				}
				if err := json.Unmarshal(raw, &input); err != nil {
					return nil, err
				}
				return input.UserIDs, nil
			},
			Handle: func(ctx context.Context, request contracts.Request, item grant.Grant) (json.RawMessage, error) {
				value, err := handle(ctx, request, service.ProviderAccess(item, s.capability(name)))
				if err != nil {
					return nil, proxy.Failure(contracts.CodePolicyDenied, err.Error())
				}
				return json.Marshal(value)
			},
		}
	}
	return []proxy.Method{
		method(ProfileGet, s.capability(ProfileGet).Scope, func(ctx context.Context, _ contracts.Request, access service.Access) (interface{}, error) {
			result, err := s.Profile(ctx, access)
			return result.Data, err
		}),
		method(UserMe, s.capability(UserMe).Scope, func(ctx context.Context, _ contracts.Request, access service.Access) (interface{}, error) {
			result, err := s.Self(ctx, access)
			return result.Data, err
		}),
		method(UserGet, s.capability(UserGet).Scope, func(ctx context.Context, request contracts.Request, access service.Access) (interface{}, error) {
			var input struct {
				UserIDs []string `json:"user_ids"`
			}
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Users(ctx, access, input.UserIDs)
			return result.Data, err
		}),
		method(DaemonGet, s.capability(DaemonGet).Scope, func(ctx context.Context, _ contracts.Request, access service.Access) (interface{}, error) {
			result, err := s.Daemon(ctx, access)
			return result.Data, err
		}),
		method(DoctorGet, s.capability(DoctorGet).Scope, func(ctx context.Context, _ contracts.Request, access service.Access) (interface{}, error) {
			result, err := s.Doctor(ctx, access)
			return result.Data, err
		}),
	}
}
