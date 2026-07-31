// Package conversation implements typed conversation reads and opaque paging.
package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abd-im-cli/abdim-cli/internal/agent/grant"
	"github.com/abd-im-cli/abdim-cli/internal/agent/proxy"
	"github.com/abd-im-cli/abdim-cli/internal/contracts"
	"github.com/abd-im-cli/abdim-cli/internal/service"
)

const (
	ListMethod   = "conversation.list"
	GetMethod    = "conversation.get"
	SearchMethod = "conversation.search"
	UnreadMethod = "conversation.unread"
	ReadScope    = "conversation.read"
)

type Conversation struct {
	ID            string    `json:"id"`
	Type          string    `json:"type,omitempty"`
	Name          string    `json:"name,omitempty"`
	UserID        string    `json:"user_id,omitempty"`
	GroupID       string    `json:"group_id,omitempty"`
	UnreadCount   int       `json:"unread_count"`
	LastMessageID string    `json:"last_message_id,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
	Pinned        bool      `json:"pinned,omitempty"`
	Muted         bool      `json:"muted,omitempty"`
	Hidden        bool      `json:"hidden,omitempty"`
}

type ListInput struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitempty"`
}

type GetInput struct {
	ConversationID string `json:"conversation_id"`
}

type SearchInput struct {
	Query  string `json:"query"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitempty"`
}

type Source interface {
	List(context.Context) ([]Conversation, error)
	Get(context.Context, string) (Conversation, error)
	Search(context.Context, string) ([]Conversation, error)
	Unread(context.Context) (int, error)
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
		return nil, errors.New("conversation source is required")
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

func (s *Service) List(ctx context.Context, access service.Access, input ListInput) (service.PageResult[Conversation], error) {
	if err := service.ValidateLimit(input.Limit); err != nil {
		return service.PageResult[Conversation]{}, err
	}
	query := "list"
	offset, err := service.DecodeCursor(input.Cursor, query)
	if err != nil {
		return service.PageResult[Conversation]{}, err
	}
	meta, err := s.authorize(access, ListMethod)
	if err != nil {
		return service.PageResult[Conversation]{}, err
	}
	items, err := s.source.List(ctx)
	if err != nil {
		return service.PageResult[Conversation]{}, fmt.Errorf("list conversations: %w", err)
	}
	items = restrict(items, access)
	page, err := page(items, offset, input.Limit, query)
	if err != nil {
		return service.PageResult[Conversation]{}, err
	}
	return service.PageResult[Conversation]{Data: page, Meta: meta}, nil
}

func (s *Service) Get(ctx context.Context, access service.Access, input GetInput) (service.Result[Conversation], error) {
	if strings.TrimSpace(input.ConversationID) == "" {
		return service.Result[Conversation]{}, fmt.Errorf("%w: conversation ID is required", service.ErrInvalidArgument)
	}
	meta, err := s.authorize(access, GetMethod, input.ConversationID)
	if err != nil {
		return service.Result[Conversation]{}, err
	}
	item, err := s.source.Get(ctx, input.ConversationID)
	if err != nil {
		return service.Result[Conversation]{}, fmt.Errorf("get conversation: %w", err)
	}
	if item.ID == "" {
		item.ID = input.ConversationID
	} else if item.ID != input.ConversationID {
		return service.Result[Conversation]{}, fmt.Errorf("%w: source returned a different conversation", service.ErrTargetDenied)
	}
	return service.Result[Conversation]{Data: item, Meta: meta}, nil
}

func (s *Service) Search(ctx context.Context, access service.Access, input SearchInput) (service.PageResult[Conversation], error) {
	if strings.TrimSpace(input.Query) == "" || len(input.Query) > 256 {
		return service.PageResult[Conversation]{}, fmt.Errorf("%w: search query must contain 1-256 characters", service.ErrInvalidArgument)
	}
	if err := service.ValidateLimit(input.Limit); err != nil {
		return service.PageResult[Conversation]{}, err
	}
	query := "search:" + input.Query
	offset, err := service.DecodeCursor(input.Cursor, query)
	if err != nil {
		return service.PageResult[Conversation]{}, err
	}
	meta, err := s.authorize(access, SearchMethod)
	if err != nil {
		return service.PageResult[Conversation]{}, err
	}
	items, err := s.source.Search(ctx, input.Query)
	if err != nil {
		return service.PageResult[Conversation]{}, fmt.Errorf("search conversations: %w", err)
	}
	items = restrict(items, access)
	page, err := page(items, offset, input.Limit, query)
	if err != nil {
		return service.PageResult[Conversation]{}, err
	}
	return service.PageResult[Conversation]{Data: page, Meta: meta}, nil
}

func (s *Service) Unread(ctx context.Context, access service.Access) (service.Result[int], error) {
	meta, err := s.authorize(access, UnreadMethod)
	if err != nil {
		return service.Result[int]{}, err
	}
	count, err := s.source.Unread(ctx)
	if err != nil {
		return service.Result[int]{}, fmt.Errorf("read unread count: %w", err)
	}
	if count < 0 {
		return service.Result[int]{}, fmt.Errorf("%w: unread count is negative", service.ErrInvalidArgument)
	}
	return service.Result[int]{Data: count, Meta: meta}, nil
}

func restrict(items []Conversation, access service.Access) []Conversation {
	if access.Owner {
		return items
	}
	result := make([]Conversation, 0, len(items))
	for _, item := range items {
		if access.Grant.AllowsTarget(item.ID) {
			result = append(result, item)
		}
	}
	return result
}

func page(items []Conversation, offset, limit int, query string) (service.Page[Conversation], error) {
	if offset > len(items) {
		return service.Page[Conversation]{}, fmt.Errorf("%w: cursor offset", service.ErrCursorExpired)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	result := service.Page[Conversation]{Items: append([]Conversation(nil), items[offset:end]...)}
	if end < len(items) {
		result.NextCursor, _ = service.EncodeCursor(query, end)
	}
	return result, nil
}

func (s *Service) Methods() []proxy.Method {
	wrap := func(name, scope string, targets func(json.RawMessage) ([]string, error), handle func(context.Context, contracts.Request, grant.Grant) (interface{}, error)) proxy.Method {
		return proxy.Method{Name: name, Scope: scope, Meta: func() contracts.Meta {
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
	getTargets := func(raw json.RawMessage) ([]string, error) {
		var input GetInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return []string{input.ConversationID}, nil
	}
	return []proxy.Method{
		wrap(ListMethod, s.capability(ListMethod).Scope, noTargets, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input ListInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.List(ctx, service.ProviderAccess(item, s.capability(ListMethod)), input)
			return result.Data, err
		}),
		wrap(GetMethod, s.capability(GetMethod).Scope, getTargets, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input GetInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Get(ctx, service.ProviderAccess(item, s.capability(GetMethod)), input)
			return result.Data, err
		}),
		wrap(SearchMethod, s.capability(SearchMethod).Scope, noTargets, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input SearchInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Search(ctx, service.ProviderAccess(item, s.capability(SearchMethod)), input)
			return result.Data, err
		}),
		wrap(UnreadMethod, s.capability(UnreadMethod).Scope, noTargets, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			result, err := s.Unread(ctx, service.ProviderAccess(item, s.capability(UnreadMethod)))
			return result.Data, err
		}),
	}
}
