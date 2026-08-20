// Package social implements typed friend and blacklist reads.
package social

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abd-im/abd-im-cli/internal/service"
)

const (
	FriendListMethod   = "friend.list"
	FriendGetMethod    = "friend.get"
	FriendSearchMethod = "friend.search"
	BlackListMethod    = "blacklist.list"
	BlackGetMethod     = "blacklist.get"
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
	ProfileID string
	Stale     func() bool
}
type Service struct {
	source  Source
	options Options
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
	return &Service{source: source, options: options}, nil
}

func (s *Service) meta() service.Meta {
	return service.NewMeta(s.options.ProfileID, s.options.Stale())
}

func (s *Service) Friends(ctx context.Context, input ListInput) (service.PageResult[Friend], error) {
	if err := service.ValidateLimit(input.Limit); err != nil {
		return service.PageResult[Friend]{}, err
	}
	offset, err := service.DecodeCursor(input.Cursor, "friends")
	if err != nil {
		return service.PageResult[Friend]{}, err
	}
	items, err := s.source.Friends(ctx)
	if err != nil {
		return service.PageResult[Friend]{}, fmt.Errorf("list friends: %w", err)
	}
	page, err := friendPage(items, offset, input.Limit, "friends")
	if err != nil {
		return service.PageResult[Friend]{}, err
	}
	return service.PageResult[Friend]{Data: page, Meta: s.meta()}, nil
}

func (s *Service) Friend(ctx context.Context, input GetInput) (service.Result[Friend], error) {
	if strings.TrimSpace(input.UserID) == "" {
		return service.Result[Friend]{}, fmt.Errorf("%w: user ID is required", service.ErrInvalidArgument)
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
	return service.Result[Friend]{Data: item, Meta: s.meta()}, nil
}

func (s *Service) SearchFriends(ctx context.Context, input SearchInput) (service.PageResult[Friend], error) {
	if err := validSearch(input.Query, input.Limit); err != nil {
		return service.PageResult[Friend]{}, err
	}
	query := "friend-search:" + input.Query
	offset, err := service.DecodeCursor(input.Cursor, query)
	if err != nil {
		return service.PageResult[Friend]{}, err
	}
	items, err := s.source.SearchFriends(ctx, input.Query)
	if err != nil {
		return service.PageResult[Friend]{}, fmt.Errorf("search friends: %w", err)
	}
	page, err := friendPage(items, offset, input.Limit, query)
	if err != nil {
		return service.PageResult[Friend]{}, err
	}
	return service.PageResult[Friend]{Data: page, Meta: s.meta()}, nil
}

func (s *Service) Blacklist(ctx context.Context, input ListInput) (service.PageResult[BlacklistEntry], error) {
	if err := service.ValidateLimit(input.Limit); err != nil {
		return service.PageResult[BlacklistEntry]{}, err
	}
	offset, err := service.DecodeCursor(input.Cursor, "blacklist")
	if err != nil {
		return service.PageResult[BlacklistEntry]{}, err
	}
	items, err := s.source.Blacklist(ctx)
	if err != nil {
		return service.PageResult[BlacklistEntry]{}, fmt.Errorf("list blacklist: %w", err)
	}
	page, err := blackPage(items, offset, input.Limit, "blacklist")
	if err != nil {
		return service.PageResult[BlacklistEntry]{}, err
	}
	return service.PageResult[BlacklistEntry]{Data: page, Meta: s.meta()}, nil
}

func (s *Service) Black(ctx context.Context, input GetInput) (service.Result[BlacklistEntry], error) {
	if strings.TrimSpace(input.UserID) == "" {
		return service.Result[BlacklistEntry]{}, fmt.Errorf("%w: user ID is required", service.ErrInvalidArgument)
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
	return service.Result[BlacklistEntry]{Data: item, Meta: s.meta()}, nil
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
