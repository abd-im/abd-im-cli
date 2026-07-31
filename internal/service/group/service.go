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
	ReadScope           = "group.read"
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
	ProfileID    string
	Stale        func() bool
	Capabilities map[string]service.Capability
}
type Service struct {
	source  Source
	options Options
}

// VerifiedCapabilities returns the fixed group read surface covered by the
// controlled SDK/server integration test.
func VerifiedCapabilities(sdkVersion string) map[string]service.Capability {
	capabilities := make(map[string]service.Capability, 5)
	for _, method := range []string{ListMethod, GetMethod, SearchMethod, MembersListMethod, MembersSearchMethod} {
		capabilities[method] = service.Capability{Method: method, Scope: ReadScope, Status: "available", SDKVersion: sdkVersion}
	}
	return capabilities
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
	if options.Capabilities == nil {
		options.Capabilities = map[string]service.Capability{}
	}
	return &Service{source: source, options: options}, nil
}

func (s *Service) capability(method string) service.Capability {
	if item, ok := s.options.Capabilities[method]; ok {
		return item
	}
	return service.Capability{Method: method, Scope: ReadScope, Status: "not_validated", Reason: "method has no verified capability entry"}
}
func (s *Service) authorize(access service.Access, method string, targets ...string) (service.Meta, error) {
	capability := s.capability(method)
	if capability.Status != "available" {
		return service.Meta{}, fmt.Errorf("%w: %s", service.ErrCapabilityUnavailable, capability.Status)
	}
	if err := access.Authorize(method, capability.Scope, targets...); err != nil {
		return service.Meta{}, err
	}
	return service.NewMeta(s.options.ProfileID, s.options.Stale(), capability), nil
}

func (s *Service) List(ctx context.Context, access service.Access, input ListInput) (service.PageResult[Group], error) {
	if err := service.ValidateLimit(input.Limit); err != nil {
		return service.PageResult[Group]{}, err
	}
	offset, err := service.DecodeCursor(input.Cursor, "list")
	if err != nil {
		return service.PageResult[Group]{}, err
	}
	meta, err := s.authorize(access, ListMethod)
	if err != nil {
		return service.PageResult[Group]{}, err
	}
	items, err := s.source.List(ctx)
	if err != nil {
		return service.PageResult[Group]{}, fmt.Errorf("list groups: %w", err)
	}
	items = allowedGroups(items, access)
	page, err := groupPage(items, offset, input.Limit, "list")
	if err != nil {
		return service.PageResult[Group]{}, err
	}
	return service.PageResult[Group]{Data: page, Meta: meta}, nil
}

func (s *Service) Get(ctx context.Context, access service.Access, input GetInput) (service.Result[Group], error) {
	if strings.TrimSpace(input.GroupID) == "" {
		return service.Result[Group]{}, fmt.Errorf("%w: group ID is required", service.ErrInvalidArgument)
	}
	meta, err := s.authorize(access, GetMethod, input.GroupID)
	if err != nil {
		return service.Result[Group]{}, err
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
	return service.Result[Group]{Data: item, Meta: meta}, nil
}

func (s *Service) Search(ctx context.Context, access service.Access, input SearchInput) (service.PageResult[Group], error) {
	if err := validSearch(input.Query, input.Limit); err != nil {
		return service.PageResult[Group]{}, err
	}
	query := "search:" + input.Query
	offset, err := service.DecodeCursor(input.Cursor, query)
	if err != nil {
		return service.PageResult[Group]{}, err
	}
	meta, err := s.authorize(access, SearchMethod)
	if err != nil {
		return service.PageResult[Group]{}, err
	}
	items, err := s.source.Search(ctx, input.Query)
	if err != nil {
		return service.PageResult[Group]{}, fmt.Errorf("search groups: %w", err)
	}
	items = allowedGroups(items, access)
	page, err := groupPage(items, offset, input.Limit, query)
	if err != nil {
		return service.PageResult[Group]{}, err
	}
	return service.PageResult[Group]{Data: page, Meta: meta}, nil
}

func (s *Service) Members(ctx context.Context, access service.Access, input MembersInput) (service.PageResult[Member], error) {
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
	meta, err := s.authorize(access, MembersListMethod, input.GroupID)
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
	return service.PageResult[Member]{Data: page, Meta: meta}, nil
}

func (s *Service) SearchMembers(ctx context.Context, access service.Access, input MembersSearchInput) (service.PageResult[Member], error) {
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
	meta, err := s.authorize(access, MembersSearchMethod, input.GroupID)
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
	return service.PageResult[Member]{Data: page, Meta: meta}, nil
}

func allowedGroups(items []Group, access service.Access) []Group {
	if access.Owner {
		return items
	}
	result := make([]Group, 0, len(items))
	for _, item := range items {
		if access.Grant.AllowsTarget(item.ID) {
			result = append(result, item)
		}
	}
	return result
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
	wrap := func(name string, targets func(json.RawMessage) ([]string, error), handle func(context.Context, contracts.Request, grant.Grant) (interface{}, error)) proxy.Method {
		return proxy.Method{Name: name, Scope: s.capability(name).Scope, Meta: func() contracts.Meta {
			return service.ContractMeta(service.NewMeta(s.options.ProfileID, s.options.Stale(), s.capability(name)))
		}, Targets: targets, Handle: func(ctx context.Context, request contracts.Request, item grant.Grant) (json.RawMessage, error) {
			value, err := handle(ctx, request, item)
			if err != nil {
				return nil, proxy.Failure(contracts.CodePolicyDenied, err.Error())
			}
			return json.Marshal(value)
		}}
	}
	noTargets := func(json.RawMessage) ([]string, error) { return nil, nil }
	groupTarget := func(raw json.RawMessage) ([]string, error) {
		var input struct {
			GroupID string `json:"group_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return []string{input.GroupID}, nil
	}
	return []proxy.Method{
		wrap(ListMethod, noTargets, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input ListInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.List(ctx, service.ProviderAccess(item, s.capability(ListMethod)), input)
			return result.Data, err
		}),
		wrap(GetMethod, groupTarget, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input GetInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Get(ctx, service.ProviderAccess(item, s.capability(GetMethod)), input)
			return result.Data, err
		}),
		wrap(SearchMethod, noTargets, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input SearchInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Search(ctx, service.ProviderAccess(item, s.capability(SearchMethod)), input)
			return result.Data, err
		}),
		wrap(MembersListMethod, groupTarget, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input MembersInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Members(ctx, service.ProviderAccess(item, s.capability(MembersListMethod)), input)
			return result.Data, err
		}),
		wrap(MembersSearchMethod, groupTarget, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input MembersSearchInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.SearchMembers(ctx, service.ProviderAccess(item, s.capability(MembersSearchMethod)), input)
			return result.Data, err
		}),
	}
}
