// Package message implements typed message reads.
package message

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abd-im/abd-im-cli/internal/service"
)

const (
	HistoryMethod = "message.history"
	SearchMethod  = "message.search"
	GetMethod     = "message.get"
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
	ProfileID string
	Stale     func() bool
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
	return &Service{source: source, options: options}, nil
}

func (s *Service) meta(conversationID string) (service.Meta, error) {
	if strings.TrimSpace(conversationID) == "" {
		return service.Meta{}, fmt.Errorf("%w: conversation ID is required", service.ErrInvalidArgument)
	}
	return service.NewMeta(s.options.ProfileID, s.options.Stale()), nil
}

func (s *Service) History(ctx context.Context, input HistoryInput) (service.PageResult[Message], error) {
	if err := service.ValidateLimit(input.Limit); err != nil {
		return service.PageResult[Message]{}, err
	}
	query := "history:" + input.ConversationID
	offset, err := service.DecodeCursor(input.Cursor, query)
	if err != nil {
		return service.PageResult[Message]{}, err
	}
	meta, err := s.meta(input.ConversationID)
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
	result, err := makePage(items, offset, input.Limit, query)
	if err != nil {
		return service.PageResult[Message]{}, err
	}
	return service.PageResult[Message]{Data: result, Meta: meta}, nil
}

func (s *Service) Search(ctx context.Context, input SearchInput) (service.PageResult[Message], error) {
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
	meta, err := s.meta(input.ConversationID)
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
	result, err := makePage(items, offset, input.Limit, query)
	if err != nil {
		return service.PageResult[Message]{}, err
	}
	return service.PageResult[Message]{Data: result, Meta: meta}, nil
}

func (s *Service) Get(ctx context.Context, input GetInput) (service.Result[Message], error) {
	if strings.TrimSpace(input.MessageID) == "" {
		return service.Result[Message]{}, fmt.Errorf("%w: message ID is required", service.ErrInvalidArgument)
	}
	meta, err := s.meta(input.ConversationID)
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
