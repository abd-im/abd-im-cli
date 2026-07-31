// Package message implements typed, grant-bounded message reads.
package message

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
	HistoryMethod = "message.history"
	SearchMethod  = "message.search"
	GetMethod     = "message.get"
	ReadScope     = "message.read"
)

type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	SenderID       string    `json:"sender_id,omitempty"`
	Type           string    `json:"type,omitempty"`
	Text           string    `json:"text,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	Revoked        bool      `json:"revoked,omitempty"`
}

type HistoryInput struct {
	ConversationID string `json:"conversation_id"`
	Limit          int    `json:"limit"`
	Cursor         string `json:"cursor,omitempty"`
}

type SearchInput struct {
	ConversationID string `json:"conversation_id"`
	Query          string `json:"query"`
	Limit          int    `json:"limit"`
	Cursor         string `json:"cursor,omitempty"`
}

type GetInput struct {
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
}

type HistoryQuery struct {
	ConversationID string
	Limit          int
}

type Source interface {
	History(context.Context, HistoryQuery) ([]Message, error)
	Search(context.Context, HistoryQuery, string) ([]Message, error)
	Get(context.Context, string, string) (Message, error)
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
		return nil, errors.New("message source is required")
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

func (s *Service) authorize(access service.Access, method, conversationID string) (service.Meta, error) {
	if strings.TrimSpace(conversationID) == "" {
		return service.Meta{}, fmt.Errorf("%w: conversation ID is required", service.ErrInvalidArgument)
	}
	capability := s.capability(method)
	if capability.Status != "available" {
		return service.Meta{}, fmt.Errorf("%w: %s", service.ErrCapabilityUnavailable, capability.Status)
	}
	if err := access.Authorize(method, capability.Scope, conversationID); err != nil {
		return service.Meta{}, err
	}
	if !access.Owner {
		window := access.Grant.MessageWindow
		if window.ConversationID != "" && window.ConversationID != conversationID {
			return service.Meta{}, fmt.Errorf("%w: grant message window", service.ErrTargetDenied)
		}
	}
	return service.NewMeta(s.options.ProfileID, s.options.Stale(), capability), nil
}

func (s *Service) History(ctx context.Context, access service.Access, input HistoryInput) (service.PageResult[Message], error) {
	if err := service.ValidateLimit(input.Limit); err != nil {
		return service.PageResult[Message]{}, err
	}
	query := "history:" + input.ConversationID
	offset, err := service.DecodeCursor(input.Cursor, query)
	if err != nil {
		return service.PageResult[Message]{}, err
	}
	meta, err := s.authorize(access, HistoryMethod, input.ConversationID)
	if err != nil {
		return service.PageResult[Message]{}, err
	}
	items, err := s.source.History(ctx, HistoryQuery{ConversationID: input.ConversationID, Limit: 100})
	if err != nil {
		return service.PageResult[Message]{}, fmt.Errorf("read message history: %w", err)
	}
	if err := validateConversation(items, input.ConversationID); err != nil {
		return service.PageResult[Message]{}, err
	}
	items, err = applyWindow(items, access)
	if err != nil {
		return service.PageResult[Message]{}, err
	}
	result, err := makePage(items, offset, input.Limit, query)
	if err != nil {
		return service.PageResult[Message]{}, err
	}
	return service.PageResult[Message]{Data: result, Meta: meta}, nil
}

func (s *Service) Search(ctx context.Context, access service.Access, input SearchInput) (service.PageResult[Message], error) {
	if strings.TrimSpace(input.Query) == "" || len(input.Query) > 256 {
		return service.PageResult[Message]{}, fmt.Errorf("%w: search query must contain 1-256 characters", service.ErrInvalidArgument)
	}
	if err := service.ValidateLimit(input.Limit); err != nil {
		return service.PageResult[Message]{}, err
	}
	query := "search:" + input.ConversationID + ":" + input.Query
	offset, err := service.DecodeCursor(input.Cursor, query)
	if err != nil {
		return service.PageResult[Message]{}, err
	}
	meta, err := s.authorize(access, SearchMethod, input.ConversationID)
	if err != nil {
		return service.PageResult[Message]{}, err
	}
	items, err := s.source.Search(ctx, HistoryQuery{ConversationID: input.ConversationID, Limit: 100}, input.Query)
	if err != nil {
		return service.PageResult[Message]{}, fmt.Errorf("search messages: %w", err)
	}
	if err := validateConversation(items, input.ConversationID); err != nil {
		return service.PageResult[Message]{}, err
	}
	items, err = applyWindow(items, access)
	if err != nil {
		return service.PageResult[Message]{}, err
	}
	result, err := makePage(items, offset, input.Limit, query)
	if err != nil {
		return service.PageResult[Message]{}, err
	}
	return service.PageResult[Message]{Data: result, Meta: meta}, nil
}

func (s *Service) Get(ctx context.Context, access service.Access, input GetInput) (service.Result[Message], error) {
	if strings.TrimSpace(input.MessageID) == "" {
		return service.Result[Message]{}, fmt.Errorf("%w: message ID is required", service.ErrInvalidArgument)
	}
	meta, err := s.authorize(access, GetMethod, input.ConversationID)
	if err != nil {
		return service.Result[Message]{}, err
	}
	item, err := s.source.Get(ctx, input.ConversationID, input.MessageID)
	if err != nil {
		return service.Result[Message]{}, fmt.Errorf("get message: %w", err)
	}
	if item.ID != input.MessageID || item.ConversationID != input.ConversationID {
		return service.Result[Message]{}, fmt.Errorf("%w: source returned a different message", service.ErrTargetDenied)
	}
	if !access.Owner && (access.Grant.MessageWindow.AfterMessageID != "" || access.Grant.MessageWindow.BeforeMessageID != "") {
		history, err := s.source.History(ctx, HistoryQuery{ConversationID: input.ConversationID, Limit: 100})
		if err != nil {
			return service.Result[Message]{}, fmt.Errorf("verify message window: %w", err)
		}
		if err := validateConversation(history, input.ConversationID); err != nil {
			return service.Result[Message]{}, err
		}
		allowed, err := applyWindow(history, access)
		if err != nil {
			return service.Result[Message]{}, err
		}
		found := false
		for _, candidate := range allowed {
			if candidate.ID == item.ID {
				found = true
				break
			}
		}
		if !found {
			return service.Result[Message]{}, fmt.Errorf("%w: message is outside grant window", service.ErrTargetDenied)
		}
	}
	return service.Result[Message]{Data: item, Meta: meta}, nil
}

func validateConversation(items []Message, conversationID string) error {
	for _, item := range items {
		if item.ConversationID != conversationID {
			return fmt.Errorf("%w: source returned a different conversation", service.ErrTargetDenied)
		}
	}
	return nil
}

func applyWindow(items []Message, access service.Access) ([]Message, error) {
	if access.Owner {
		return items, nil
	}
	window := access.Grant.MessageWindow
	if window.ConversationID == "" {
		return nil, fmt.Errorf("%w: grant message window is required", service.ErrTargetDenied)
	}
	start, end := 0, len(items)
	if window.AfterMessageID != "" {
		found := false
		for i, item := range items {
			if item.ID == window.AfterMessageID {
				start, found = i+1, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: after message boundary", service.ErrTargetDenied)
		}
	}
	if window.BeforeMessageID != "" {
		found := false
		for i, item := range items {
			if item.ID == window.BeforeMessageID {
				end, found = i, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: before message boundary", service.ErrTargetDenied)
		}
	}
	if start > end {
		return nil, fmt.Errorf("%w: message window is empty", service.ErrTargetDenied)
	}
	return append([]Message(nil), items[start:end]...), nil
}

func makePage(items []Message, offset, limit int, query string) (service.Page[Message], error) {
	if offset > len(items) {
		return service.Page[Message]{}, fmt.Errorf("%w: cursor offset", service.ErrCursorExpired)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	result := service.Page[Message]{Items: append([]Message(nil), items[offset:end]...)}
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
	historyTargets := func(raw json.RawMessage) ([]string, error) {
		var input HistoryInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return []string{input.ConversationID}, nil
	}
	searchTargets := func(raw json.RawMessage) ([]string, error) {
		var input SearchInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return []string{input.ConversationID}, nil
	}
	getTargets := func(raw json.RawMessage) ([]string, error) {
		var input GetInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return []string{input.ConversationID}, nil
	}
	return []proxy.Method{
		wrap(HistoryMethod, historyTargets, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input HistoryInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.History(ctx, service.ProviderAccess(item, s.capability(HistoryMethod)), input)
			return result.Data, err
		}),
		wrap(SearchMethod, searchTargets, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input SearchInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Search(ctx, service.ProviderAccess(item, s.capability(SearchMethod)), input)
			return result.Data, err
		}),
		wrap(GetMethod, getTargets, func(ctx context.Context, request contracts.Request, item grant.Grant) (interface{}, error) {
			var input GetInput
			if err := json.Unmarshal(request.Params, &input); err != nil {
				return nil, service.ErrInvalidArgument
			}
			result, err := s.Get(ctx, service.ProviderAccess(item, s.capability(GetMethod)), input)
			return result.Data, err
		}),
	}
}
