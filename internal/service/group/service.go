// Package group implements typed group and group-member reads.
package group

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
	ListMethod          = "group.list"
	GetMethod           = "group.get"
	SearchMethod        = "group.search"
	MembersListMethod   = "group.members.list"
	MembersSearchMethod = "group.members.search"
)

type Group struct {
	ID          string    `json:"id"`
	Name        string    `json:"name,omitempty"`
	OwnerID     string    `json:"owner_id,omitempty"`
	MemberCount int       `json:"member_count,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
}

type Member struct {
	GroupID  string    `json:"group_id"`
	UserID   string    `json:"user_id"`
	Nickname string    `json:"nickname,omitempty"`
	Role     string    `json:"role,omitempty"`
	JoinedAt time.Time `json:"joined_at,omitempty"`
	Muted    bool      `json:"muted,omitempty"`
}

type ListInput struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitempty"`
}
type GetInput struct {
	GroupID string `json:"group_id"`
}
type SearchInput struct {
	Query  string `json:"query"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitempty"`
}
type MembersInput struct {
	GroupID string `json:"group_id"`
	Limit   int    `json:"limit"`
	Cursor  string `json:"cursor,omitempty"`
}
type MembersSearchInput struct {
	GroupID string `json:"group_id"`
	Query   string `json:"query"`
	Limit   int    `json:"limit"`
	Cursor  string `json:"cursor,omitempty"`
}

// Source is a daemon-owned facade over verified SDK public APIs.
type Source interface {
	List(context.Context) ([]Group, error)
	Get(context.Context, string) (Group, error)
	Search(context.Context, string) ([]Group, error)
	Members(context.Context, string) ([]Member, error)
	SearchMembers(context.Context, string, string) ([]Member, error)
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
		return nil, errors.New("group source is required")
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

func (s *Service) List(ctx context.Context, _ service.Access, input ListInput) (service.PageResult[Group], error) {
	if err := service.ValidateLimit(input.Limit); err != nil {
		return service.PageResult[Group]{}, err
	}
	offset, err := service.DecodeCursor(input.Cursor, "list")
	if err != nil {
		return service.PageResult[Group]{}, err
	}
	items, err := s.source.List(ctx)
	if err != nil {
		return service.PageResult[Group]{}, fmt.Errorf("list groups: %w", err)
	}
	page, err := groupPage(items, offset, input.Limit, "list")
	if err != nil {
		return service.PageResult[Group]{}, err
	}
	return service.PageResult[Group]{Data: page, Meta: s.meta()}, nil
}

func (s *Service) Get(ctx context.Context, _ service.Access, input GetInput) (service.Result[Group], error) {
	if strings.TrimSpace(input.GroupID) == "" {
		return service.Result[Group]{}, fmt.Errorf("%w: group ID is required", service.ErrInvalidArgument)
	}
	item, err := s.source.Get(ctx, input.GroupID)
	if err != nil {
		return service.Result[Group]{}, fmt.Errorf("get group: %w", err)
	}
	if item.ID == "" {
		item.ID = input.GroupID
	} else if item.ID != input.GroupID {
		return service.Result[Group]{}, fmt.Errorf("%w: source returned a different group", service.ErrTargetDenied)
	}
	return service.Result[Group]{Data: item, Meta: s.meta()}, nil
}

func (s *Service) Search(ctx context.Context, _ service.Access, input SearchInput) (service.PageResult[Group], error) {
	if err := validSearch(input.Query, input.Limit); err != nil {
		return service.PageResult[Group]{}, err
	}
	query := "search:" + input.Query
	offset, err := service.DecodeCursor(input.Cursor, query)
	if err != nil {
		return service.PageResult[Group]{}, err
	}
	items, err := s.source.Search(ctx, input.Query)
	if err != nil {
		return service.PageResult[Group]{}, fmt.Errorf("search groups: %w", err)
	}
	page, err := groupPage(items, offset, input.Limit, query)
	if err != nil {
		return service.PageResult[Group]{}, err
	}
	return service.PageResult[Group]{Data: page, Meta: s.meta()}, nil
}

func (s *Service) Members(ctx context.Context, _ service.Access, input MembersInput) (service.PageResult[Member], error) {
	if strings.TrimSpace(input.GroupID) == "" {
		return service.PageResult[Member]{}, fmt.Errorf("%w: group ID is required", service.ErrInvalidArgument)
	}
	if err := service.ValidateLimit(input.Limit); err != nil {
		return service.PageResult[Member]{}, err
	}
	query := "members:" + input.GroupID
	offset, err := service.DecodeCursor(input.Cursor, query)
	if err != nil {
		return service.PageResult[Member]{}, err
	}
	items, err := s.source.Members(ctx, input.GroupID)
	if err != nil {
		return service.PageResult[Member]{}, fmt.Errorf("list group members: %w", err)
	}
	for _, item := range items {
		if item.GroupID != input.GroupID {
			return service.PageResult[Member]{}, fmt.Errorf("%w: source returned different group", service.ErrTargetDenied)
		}
	}
	page, err := memberPage(items, offset, input.Limit, query)
	if err != nil {
		return service.PageResult[Member]{}, err
	}
	return service.PageResult[Member]{Data: page, Meta: s.meta()}, nil
}

func (s *Service) SearchMembers(ctx context.Context, _ service.Access, input MembersSearchInput) (service.PageResult[Member], error) {
	if strings.TrimSpace(input.GroupID) == "" {
		return service.PageResult[Member]{}, fmt.Errorf("%w: group ID is required", service.ErrInvalidArgument)
	}
	if err := validSearch(input.Query, input.Limit); err != nil {
		return service.PageResult[Member]{}, err
	}
	query := "member-search:" + input.GroupID + ":" + input.Query
	offset, err := service.DecodeCursor(input.Cursor, query)
	if err != nil {
		return service.PageResult[Member]{}, err
	}
	items, err := s.source.SearchMembers(ctx, input.GroupID, input.Query)
	if err != nil {
		return service.PageResult[Member]{}, fmt.Errorf("search group members: %w", err)
	}
	for _, item := range items {
		if item.GroupID != input.GroupID {
			return service.PageResult[Member]{}, fmt.Errorf("%w: source returned different group", service.ErrTargetDenied)
		}
	}
	page, err := memberPage(items, offset, input.Limit, query)
	if err != nil {
		return service.PageResult[Member]{}, err
	}
	return service.PageResult[Member]{Data: page, Meta: s.meta()}, nil
}

func validSearch(query string, limit int) error {
	if strings.TrimSpace(query) == "" || len(query) > 256 {
		return fmt.Errorf("%w: search query must contain 1-256 characters", service.ErrInvalidArgument)
	}
	return service.ValidateLimit(limit)
}
func groupPage(items []Group, offset, limit int, query string) (service.Page[Group], error) {
	if offset > len(items) {
		return service.Page[Group]{}, fmt.Errorf("%w: cursor offset", service.ErrCursorExpired)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	result := service.Page[Group]{Items: append([]Group(nil), items[offset:end]...)}
	if end < len(items) {
		result.NextCursor, _ = service.EncodeCursor(query, end)
	}
	return result, nil
}
func memberPage(items []Member, offset, limit int, query string) (service.Page[Member], error) {
	if offset > len(items) {
		return service.Page[Member]{}, fmt.Errorf("%w: cursor offset", service.ErrCursorExpired)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	result := service.Page[Member]{Items: append([]Member(nil), items[offset:end]...)}
	if end < len(items) {
		result.NextCursor, _ = service.EncodeCursor(query, end)
	}
	return result, nil
}

func (s *Service) Methods() []proxy.Method {
	wrap := func(name string, handle func(context.Context, contracts.Request, grant.Grant) (interface{}, error)) proxy.Method {
		return proxy.Method{Name: name, Meta: func() contracts.Meta {
			return service.ContractMeta(s.meta())
		}, Handle: func(ctx context.Context, request contracts.Request, item grant.Grant) (json.RawMessage, error) {
			value, err := handle(ctx, request, item)
			if err != nil {
				return nil, proxy.Failure(contracts.CodePolicyDenied, err.Error())
			}
			return json.Marshal(value)
		}}
	}
	return []proxy.Method{
		wrap(ListMethod, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input ListInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.List(ctx, service.ProviderAccess(item), input)
			return result.Data, err
		}),
		wrap(GetMethod, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input GetInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Get(ctx, service.ProviderAccess(item), input)
			return result.Data, err
		}),
		wrap(SearchMethod, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input SearchInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Search(ctx, service.ProviderAccess(item), input)
			return result.Data, err
		}),
		wrap(MembersListMethod, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input MembersInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Members(ctx, service.ProviderAccess(item), input)
			return result.Data, err
		}),
		wrap(MembersSearchMethod, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input MembersSearchInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.SearchMembers(ctx, service.ProviderAccess(item), input)
			return result.Data, err
		}),
	}
}
