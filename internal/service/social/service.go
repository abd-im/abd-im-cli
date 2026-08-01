// Package social implements typed friend and blacklist reads.
package social

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
	FriendListMethod   = "friend.list"
	FriendGetMethod    = "friend.get"
	FriendSearchMethod = "friend.search"
	BlackListMethod    = "blacklist.list"
	BlackGetMethod     = "blacklist.get"
	FriendReadScope    = "friend.read"
	BlackReadScope     = "blacklist.read"
)

type Friend struct {
	UserID   string    `json:"user_id"`
	Nickname string    `json:"nickname,omitempty"`
	Remark   string    `json:"remark,omitempty"`
	AddedAt  time.Time `json:"added_at,omitempty"`
}

type BlacklistEntry struct {
	UserID    string    `json:"user_id"`
	BlockedAt time.Time `json:"blocked_at,omitempty"`
}

type ListInput struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitempty"`
}
type GetInput struct {
	UserID string `json:"user_id"`
}
type SearchInput struct {
	Query  string `json:"query"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitempty"`
}

// Source is a daemon-owned facade over verified SDK public APIs.
type Source interface {
	Friends(context.Context) ([]Friend, error)
	Friend(context.Context, string) (Friend, error)
	SearchFriends(context.Context, string) ([]Friend, error)
	Blacklist(context.Context) ([]BlacklistEntry, error)
	Black(context.Context, string) (BlacklistEntry, error)
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

// VerifiedCapabilities returns the fixed social reads covered by the
// controlled SDK/server integration test.
func VerifiedCapabilities(sdkVersion string) map[string]service.Capability {
	capabilities := make(map[string]service.Capability, 5)
	for _, method := range []string{FriendListMethod, FriendGetMethod, FriendSearchMethod, BlackListMethod, BlackGetMethod} {
		capabilities[method] = service.Capability{Method: method, Scope: scope(method), Status: "available", SDKVersion: sdkVersion}
	}
	return capabilities
}

func New(source Source, options Options) (*Service, error) {
	if source == nil {
		return nil, errors.New("social source is required")
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

func scope(method string) string {
	if strings.HasPrefix(method, "blacklist.") {
		return BlackReadScope
	}
	return FriendReadScope
}
func (s *Service) capability(method string) service.Capability {
	if item, ok := s.options.Capabilities[method]; ok {
		return item
	}
	return service.Capability{Method: method, Scope: scope(method), Status: "not_validated", Reason: "method has no verified capability entry"}
}
func (s *Service) authorize(access service.Access, method string, targets ...string) (service.Meta, error) {
	capability := s.capability(method)
	if capability.Status != "available" {
		return service.Meta{}, fmt.Errorf("%w: %s", service.ErrCapabilityUnavailable, capability.Status)
	}
	if err := access.Authorize(method, capability.Scope, userTargets(targets)...); err != nil {
		return service.Meta{}, err
	}
	return service.NewMeta(s.options.ProfileID, s.options.Stale(), capability), nil
}

func (s *Service) Friends(ctx context.Context, access service.Access, input ListInput) (service.PageResult[Friend], error) {
	if err := service.ValidateLimit(input.Limit); err != nil {
		return service.PageResult[Friend]{}, err
	}
	offset, err := service.DecodeCursor(input.Cursor, "friends")
	if err != nil {
		return service.PageResult[Friend]{}, err
	}
	meta, err := s.authorize(access, FriendListMethod)
	if err != nil {
		return service.PageResult[Friend]{}, err
	}
	items, err := s.source.Friends(ctx)
	if err != nil {
		return service.PageResult[Friend]{}, fmt.Errorf("list friends: %w", err)
	}
	items = restrictFriends(items, access, FriendListMethod)
	page, err := friendPage(items, offset, input.Limit, "friends")
	if err != nil {
		return service.PageResult[Friend]{}, err
	}
	return service.PageResult[Friend]{Data: page, Meta: meta}, nil
}

func (s *Service) Friend(ctx context.Context, access service.Access, input GetInput) (service.Result[Friend], error) {
	if strings.TrimSpace(input.UserID) == "" {
		return service.Result[Friend]{}, fmt.Errorf("%w: user ID is required", service.ErrInvalidArgument)
	}
	meta, err := s.authorize(access, FriendGetMethod, input.UserID)
	if err != nil {
		return service.Result[Friend]{}, err
	}
	item, err := s.source.Friend(ctx, input.UserID)
	if err != nil {
		return service.Result[Friend]{}, fmt.Errorf("get friend: %w", err)
	}
	if item.UserID == "" {
		item.UserID = input.UserID
	} else if item.UserID != input.UserID {
		return service.Result[Friend]{}, fmt.Errorf("%w: source returned a different user", service.ErrTargetDenied)
	}
	return service.Result[Friend]{Data: item, Meta: meta}, nil
}

func (s *Service) SearchFriends(ctx context.Context, access service.Access, input SearchInput) (service.PageResult[Friend], error) {
	if err := validSearch(input.Query, input.Limit); err != nil {
		return service.PageResult[Friend]{}, err
	}
	query := "friend-search:" + input.Query
	offset, err := service.DecodeCursor(input.Cursor, query)
	if err != nil {
		return service.PageResult[Friend]{}, err
	}
	meta, err := s.authorize(access, FriendSearchMethod)
	if err != nil {
		return service.PageResult[Friend]{}, err
	}
	items, err := s.source.SearchFriends(ctx, input.Query)
	if err != nil {
		return service.PageResult[Friend]{}, fmt.Errorf("search friends: %w", err)
	}
	items = restrictFriends(items, access, FriendSearchMethod)
	page, err := friendPage(items, offset, input.Limit, query)
	if err != nil {
		return service.PageResult[Friend]{}, err
	}
	return service.PageResult[Friend]{Data: page, Meta: meta}, nil
}

func (s *Service) Blacklist(ctx context.Context, access service.Access, input ListInput) (service.PageResult[BlacklistEntry], error) {
	if err := service.ValidateLimit(input.Limit); err != nil {
		return service.PageResult[BlacklistEntry]{}, err
	}
	offset, err := service.DecodeCursor(input.Cursor, "blacklist")
	if err != nil {
		return service.PageResult[BlacklistEntry]{}, err
	}
	meta, err := s.authorize(access, BlackListMethod)
	if err != nil {
		return service.PageResult[BlacklistEntry]{}, err
	}
	items, err := s.source.Blacklist(ctx)
	if err != nil {
		return service.PageResult[BlacklistEntry]{}, fmt.Errorf("list blacklist: %w", err)
	}
	items = restrictBlack(items, access, BlackListMethod)
	page, err := blackPage(items, offset, input.Limit, "blacklist")
	if err != nil {
		return service.PageResult[BlacklistEntry]{}, err
	}
	return service.PageResult[BlacklistEntry]{Data: page, Meta: meta}, nil
}

func (s *Service) Black(ctx context.Context, access service.Access, input GetInput) (service.Result[BlacklistEntry], error) {
	if strings.TrimSpace(input.UserID) == "" {
		return service.Result[BlacklistEntry]{}, fmt.Errorf("%w: user ID is required", service.ErrInvalidArgument)
	}
	meta, err := s.authorize(access, BlackGetMethod, input.UserID)
	if err != nil {
		return service.Result[BlacklistEntry]{}, err
	}
	item, err := s.source.Black(ctx, input.UserID)
	if err != nil {
		return service.Result[BlacklistEntry]{}, fmt.Errorf("get blacklist entry: %w", err)
	}
	if item.UserID == "" {
		item.UserID = input.UserID
	} else if item.UserID != input.UserID {
		return service.Result[BlacklistEntry]{}, fmt.Errorf("%w: source returned a different user", service.ErrTargetDenied)
	}
	return service.Result[BlacklistEntry]{Data: item, Meta: meta}, nil
}

func restrictFriends(items []Friend, access service.Access, method string) []Friend {
	if access.Owner {
		return items
	}
	result := make([]Friend, 0, len(items))
	for _, item := range items {
		if access.Grant.AllowsTarget(method, grant.UserTarget(item.UserID)) {
			result = append(result, item)
		}
	}
	return result
}
func restrictBlack(items []BlacklistEntry, access service.Access, method string) []BlacklistEntry {
	if access.Owner {
		return items
	}
	result := make([]BlacklistEntry, 0, len(items))
	for _, item := range items {
		if access.Grant.AllowsTarget(method, grant.UserTarget(item.UserID)) {
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
func friendPage(items []Friend, offset, limit int, query string) (service.Page[Friend], error) {
	if offset > len(items) {
		return service.Page[Friend]{}, fmt.Errorf("%w: cursor offset", service.ErrCursorExpired)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	result := service.Page[Friend]{Items: append([]Friend(nil), items[offset:end]...)}
	if end < len(items) {
		result.NextCursor, _ = service.EncodeCursor(query, end)
	}
	return result, nil
}
func blackPage(items []BlacklistEntry, offset, limit int, query string) (service.Page[BlacklistEntry], error) {
	if offset > len(items) {
		return service.Page[BlacklistEntry]{}, fmt.Errorf("%w: cursor offset", service.ErrCursorExpired)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	result := service.Page[BlacklistEntry]{Items: append([]BlacklistEntry(nil), items[offset:end]...)}
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
	userTarget := func(raw json.RawMessage) ([]string, error) {
		var input GetInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return []string{grant.UserTarget(input.UserID)}, nil
	}
	return []proxy.Method{
		wrap(FriendListMethod, noTargets, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input ListInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Friends(ctx, service.ProviderAccess(item, s.capability(FriendListMethod)), input)
			return result.Data, err
		}),
		wrap(FriendGetMethod, userTarget, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input GetInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Friend(ctx, service.ProviderAccess(item, s.capability(FriendGetMethod)), input)
			return result.Data, err
		}),
		wrap(FriendSearchMethod, noTargets, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input SearchInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.SearchFriends(ctx, service.ProviderAccess(item, s.capability(FriendSearchMethod)), input)
			return result.Data, err
		}),
		wrap(BlackListMethod, noTargets, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input ListInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Blacklist(ctx, service.ProviderAccess(item, s.capability(BlackListMethod)), input)
			return result.Data, err
		}),
		wrap(BlackGetMethod, userTarget, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input GetInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Black(ctx, service.ProviderAccess(item, s.capability(BlackGetMethod)), input)
			return result.Data, err
		}),
	}
}

func userTargets(ids []string) []string {
	targets := make([]string, 0, len(ids))
	for _, id := range ids {
		targets = append(targets, grant.UserTarget(id))
	}
	return targets
}
