// Package conversation implements typed conversation reads and opaque paging.
package conversation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abd-im/abd-im-cli/internal/service"
)

const (
	ListMethod   = "conversation.list"
	GetMethod    = "conversation.get"
	SearchMethod = "conversation.search"
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
		return nil, errors.New("conversation source is required")
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

func (s *Service) List(ctx context.Context, input ListInput) (service.PageResult[Conversation], error) {
	if err := service.ValidateLimit(input.Limit); err != nil {
		return service.PageResult[Conversation]{}, err
	}
	query := "list"
	offset, err := service.DecodeCursor(input.Cursor, query)
	if err != nil {
		return service.PageResult[Conversation]{}, err
	}
	items, err := s.source.List(ctx)
	if err != nil {
		return service.PageResult[Conversation]{}, fmt.Errorf("list conversations: %w", err)
	}
	page, err := page(items, offset, input.Limit, query)
	if err != nil {
		return service.PageResult[Conversation]{}, err
	}
	return service.PageResult[Conversation]{Data: page, Meta: s.meta()}, nil
}

func (s *Service) Get(ctx context.Context, input GetInput) (service.Result[Conversation], error) {
	if strings.TrimSpace(input.ConversationID) == "" {
		return service.Result[Conversation]{}, fmt.Errorf("%w: conversation ID is required", service.ErrInvalidArgument)
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
	return service.Result[Conversation]{Data: item, Meta: s.meta()}, nil
}

func (s *Service) Search(ctx context.Context, input SearchInput) (service.PageResult[Conversation], error) {
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
	items, err := s.source.Search(ctx, input.Query)
	if err != nil {
		return service.PageResult[Conversation]{}, fmt.Errorf("search conversations: %w", err)
	}
	page, err := page(items, offset, input.Limit, query)
	if err != nil {
		return service.PageResult[Conversation]{}, err
	}
	return service.PageResult[Conversation]{Data: page, Meta: s.meta()}, nil
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
