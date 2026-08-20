// Package profile implements typed profile, user, daemon and doctor reads.
package profile

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	ProfileID string
	Stale     func() bool
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
	return &Service{source: source, options: options}, nil
}

func (s *Service) meta() service.Meta {
	return service.NewMeta(s.options.ProfileID, s.options.Stale())
}

func (s *Service) Profile(ctx context.Context) (service.Result[Profile], error) {
	item, err := s.source.Profile(ctx)
	if err != nil {
		return service.Result[Profile]{}, fmt.Errorf("read profile: %w", err)
	}
	if item.ID == "" {
		item.ID = s.options.ProfileID
	} else if item.ID != s.options.ProfileID {
		return service.Result[Profile]{}, fmt.Errorf("%w: source returned a different profile", service.ErrTargetDenied)
	}
	return service.Result[Profile]{Data: item, Meta: s.meta()}, nil
}

func (s *Service) Self(ctx context.Context) (service.Result[User], error) {
	item, err := s.source.Self(ctx)
	if err != nil {
		return service.Result[User]{}, fmt.Errorf("read self user: %w", err)
	}
	return service.Result[User]{Data: item, Meta: s.meta()}, nil
}

func (s *Service) Users(ctx context.Context, ids []string) (service.Result[[]User], error) {
	if len(ids) == 0 || len(ids) > 100 {
		return service.Result[[]User]{}, fmt.Errorf("%w: user IDs must contain 1-100 items", service.ErrInvalidArgument)
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return service.Result[[]User]{}, fmt.Errorf("%w: user ID is required", service.ErrInvalidArgument)
		}
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
	return service.Result[[]User]{Data: items, Meta: s.meta()}, nil
}

func (s *Service) Daemon(ctx context.Context) (service.Result[DaemonStatus], error) {
	item, err := s.source.Daemon(ctx)
	if err != nil {
		return service.Result[DaemonStatus]{}, fmt.Errorf("read daemon status: %w", err)
	}
	return service.Result[DaemonStatus]{Data: item, Meta: s.meta()}, nil
}

func (s *Service) Doctor(ctx context.Context) (service.Result[DoctorReport], error) {
	item, err := s.source.Doctor(ctx)
	if err != nil {
		return service.Result[DoctorReport]{}, fmt.Errorf("run doctor: %w", err)
	}
	return service.Result[DoctorReport]{Data: item, Meta: s.meta()}, nil
}
