package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/service"
	conversationservice "github.com/abd-im/abd-im-cli/internal/service/conversation"
	groupservice "github.com/abd-im/abd-im-cli/internal/service/group"
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	profileservice "github.com/abd-im/abd-im-cli/internal/service/profile"
	socialservice "github.com/abd-im/abd-im-cli/internal/service/social"
)

// OwnerServices is the complete typed read surface registered for one owner
// dispatcher. The concrete services own capability checks and data access;
// this adapter only decodes their fixed inputs and preserves their metadata.
type OwnerServices struct {
	Profile      *profileservice.Service
	Conversation *conversationservice.Service
	Message      *messageservice.Service
	Group        *groupservice.Service
	Social       *socialservice.Service
}

// OwnerMethods binds every P1 typed read to owner access. It has no generic
// service, endpoint, SDK, or database method selection path.
func OwnerMethods(services OwnerServices) ([]OwnerMethod, error) {
	if services.Profile == nil || services.Conversation == nil || services.Message == nil || services.Group == nil || services.Social == nil {
		return nil, errors.New("all owner typed services are required")
	}
	methods := make([]OwnerMethod, 0, 22)
	methods = append(methods, profileOwnerMethods(services.Profile)...)
	methods = append(methods, conversationOwnerMethods(services.Conversation)...)
	methods = append(methods, messageOwnerMethods(services.Message)...)
	methods = append(methods, groupOwnerMethods(services.Group)...)
	methods = append(methods, socialOwnerMethods(services.Social)...)
	return methods, nil
}

func profileOwnerMethods(reader *profileservice.Service) []OwnerMethod {
	return []OwnerMethod{
		ownerMethod(profileservice.ProfileGet, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			if err := decodeOwnerParams(raw, &struct{}{}); err != nil {
				return OwnerResult{}, err
			}
			return ownerResult(reader.Profile(ctx, ownerAccess()))
		}),
		ownerMethod(profileservice.UserMe, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			if err := decodeOwnerParams(raw, &struct{}{}); err != nil {
				return OwnerResult{}, err
			}
			return ownerResult(reader.Self(ctx, ownerAccess()))
		}),
		ownerMethod(profileservice.UserGet, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input struct {
				UserIDs []string `json:"user_ids"`
			}
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerResult(reader.Users(ctx, ownerAccess(), input.UserIDs))
		}),
		ownerMethod(profileservice.DaemonGet, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			if err := decodeOwnerParams(raw, &struct{}{}); err != nil {
				return OwnerResult{}, err
			}
			return ownerResult(reader.Daemon(ctx, ownerAccess()))
		}),
		ownerMethod(profileservice.DoctorGet, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			if err := decodeOwnerParams(raw, &struct{}{}); err != nil {
				return OwnerResult{}, err
			}
			return ownerResult(reader.Doctor(ctx, ownerAccess()))
		}),
	}
}

func conversationOwnerMethods(reader *conversationservice.Service) []OwnerMethod {
	return []OwnerMethod{
		ownerMethod(conversationservice.ListMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input conversationservice.ListInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerPageResult(reader.List(ctx, ownerAccess(), input))
		}),
		ownerMethod(conversationservice.GetMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input conversationservice.GetInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerResult(reader.Get(ctx, ownerAccess(), input))
		}),
		ownerMethod(conversationservice.SearchMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input conversationservice.SearchInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerPageResult(reader.Search(ctx, ownerAccess(), input))
		}),
		ownerMethod(conversationservice.UnreadMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			if err := decodeOwnerParams(raw, &struct{}{}); err != nil {
				return OwnerResult{}, err
			}
			return ownerResult(reader.Unread(ctx, ownerAccess()))
		}),
	}
}

func messageOwnerMethods(reader *messageservice.Service) []OwnerMethod {
	return []OwnerMethod{
		ownerMethod(messageservice.HistoryMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input messageservice.HistoryInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerPageResult(reader.History(ctx, ownerAccess(), input))
		}),
		ownerMethod(messageservice.SearchMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input messageservice.SearchInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerPageResult(reader.Search(ctx, ownerAccess(), input))
		}),
		ownerMethod(messageservice.GetMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input messageservice.GetInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerResult(reader.Get(ctx, ownerAccess(), input))
		}),
	}
}

func groupOwnerMethods(reader *groupservice.Service) []OwnerMethod {
	return []OwnerMethod{
		ownerMethod(groupservice.ListMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input groupservice.ListInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerPageResult(reader.List(ctx, ownerAccess(), input))
		}),
		ownerMethod(groupservice.GetMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input groupservice.GetInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerResult(reader.Get(ctx, ownerAccess(), input))
		}),
		ownerMethod(groupservice.SearchMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input groupservice.SearchInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerPageResult(reader.Search(ctx, ownerAccess(), input))
		}),
		ownerMethod(groupservice.MembersListMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input groupservice.MembersInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerPageResult(reader.Members(ctx, ownerAccess(), input))
		}),
		ownerMethod(groupservice.MembersSearchMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input groupservice.MembersSearchInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerPageResult(reader.SearchMembers(ctx, ownerAccess(), input))
		}),
	}
}

func socialOwnerMethods(reader *socialservice.Service) []OwnerMethod {
	return []OwnerMethod{
		ownerMethod(socialservice.FriendListMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input socialservice.ListInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerPageResult(reader.Friends(ctx, ownerAccess(), input))
		}),
		ownerMethod(socialservice.FriendGetMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input socialservice.GetInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerResult(reader.Friend(ctx, ownerAccess(), input))
		}),
		ownerMethod(socialservice.FriendSearchMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input socialservice.SearchInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerPageResult(reader.SearchFriends(ctx, ownerAccess(), input))
		}),
		ownerMethod(socialservice.BlackListMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input socialservice.ListInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerPageResult(reader.Blacklist(ctx, ownerAccess(), input))
		}),
		ownerMethod(socialservice.BlackGetMethod, func(ctx context.Context, raw json.RawMessage) (OwnerResult, error) {
			var input socialservice.GetInput
			if err := decodeOwnerParams(raw, &input); err != nil {
				return OwnerResult{}, err
			}
			return ownerResult(reader.Black(ctx, ownerAccess(), input))
		}),
	}
}

func ownerMethod(name string, handle func(context.Context, json.RawMessage) (OwnerResult, error)) OwnerMethod {
	return OwnerMethod{Name: name, Handle: handle}
}

func decodeOwnerParams(raw json.RawMessage, output any) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return MethodFailure(contracts.CodeInvalidArgument, "typed method parameters must be an object", false)
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return MethodFailure(contracts.CodeInvalidArgument, "invalid typed method parameters", false)
	}
	return nil
}

func ownerAccess() service.Access { return service.OwnerAccess(service.Capability{}) }

func ownerResult[T any](value service.Result[T], err error) (OwnerResult, error) {
	if err != nil {
		return OwnerResult{}, ownerServiceFailure(err)
	}
	return OwnerResult{Data: value.Data, Meta: service.ContractMeta(value.Meta)}, nil
}

func ownerPageResult[T any](value service.PageResult[T], err error) (OwnerResult, error) {
	if err != nil {
		return OwnerResult{}, ownerServiceFailure(err)
	}
	return OwnerResult{Data: value.Data, Meta: service.ContractMeta(value.Meta)}, nil
}

func ownerServiceFailure(err error) error {
	switch {
	case errors.Is(err, service.ErrInvalidArgument), errors.Is(err, service.ErrCursorInvalid):
		return MethodFailure(contracts.CodeInvalidArgument, "invalid typed service parameters", false)
	case errors.Is(err, service.ErrCursorExpired):
		return MethodFailure(contracts.CodeCursorExpired, "typed service cursor has expired", false)
	case errors.Is(err, service.ErrCapabilityUnavailable):
		return MethodFailure(contracts.CodeConnectionUnavailable, "typed service capability is unavailable", true)
	case errors.Is(err, service.ErrScopeDenied), errors.Is(err, service.ErrTargetDenied):
		return MethodFailure(contracts.CodeInternal, "typed service failed", false)
	default:
		return MethodFailure(contracts.CodeInternal, "typed service failed", false)
	}
}
