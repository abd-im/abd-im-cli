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

// Services is the complete read surface registered for one SDK identity.
type Services struct {
	Profile      *profileservice.Service
	Conversation *conversationservice.Service
	Message      *messageservice.Service
	Group        *groupservice.Service
	Social       *socialservice.Service
}

// Methods binds the fixed CLI commands to one SDK identity.
func Methods(services Services) ([]Method, error) {
	if services.Profile == nil || services.Conversation == nil || services.Message == nil || services.Group == nil || services.Social == nil {
		return nil, errors.New("all services are required")
	}
	methods := make([]Method, 0, 22)
	methods = append(methods, profileMethods(services.Profile)...)
	methods = append(methods, conversationMethods(services.Conversation)...)
	methods = append(methods, messageMethods(services.Message)...)
	methods = append(methods, groupMethods(services.Group)...)
	methods = append(methods, socialMethods(services.Social)...)
	return methods, nil
}

func profileMethods(reader *profileservice.Service) []Method {
	return []Method{
		method(profileservice.ProfileGet, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			if err := decodeParams(raw, &struct{}{}); err != nil {
				return MethodResult{}, err
			}
			return methodResult(reader.Profile(ctx))
		}),
		method(profileservice.UserMe, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			if err := decodeParams(raw, &struct{}{}); err != nil {
				return MethodResult{}, err
			}
			return methodResult(reader.Self(ctx))
		}),
		method(profileservice.UserGet, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input struct {
				UserIDs []string `json:"user_ids"`
			}
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodResult(reader.Users(ctx, input.UserIDs))
		}),
		method(profileservice.DaemonGet, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			if err := decodeParams(raw, &struct{}{}); err != nil {
				return MethodResult{}, err
			}
			return methodResult(reader.Daemon(ctx))
		}),
		method(profileservice.DoctorGet, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			if err := decodeParams(raw, &struct{}{}); err != nil {
				return MethodResult{}, err
			}
			return methodResult(reader.Doctor(ctx))
		}),
	}
}

func conversationMethods(reader *conversationservice.Service) []Method {
	return []Method{
		method(conversationservice.ListMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input conversationservice.ListInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodPageResult(reader.List(ctx, input))
		}),
		method(conversationservice.GetMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input conversationservice.GetInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodResult(reader.Get(ctx, input))
		}),
		method(conversationservice.SearchMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input conversationservice.SearchInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodPageResult(reader.Search(ctx, input))
		}),
	}
}

func messageMethods(reader *messageservice.Service) []Method {
	return []Method{
		method(messageservice.HistoryMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input messageservice.HistoryInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodPageResult(reader.History(ctx, input))
		}),
		method(messageservice.SearchMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input messageservice.SearchInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodPageResult(reader.Search(ctx, input))
		}),
		method(messageservice.GetMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input messageservice.GetInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodResult(reader.Get(ctx, input))
		}),
	}
}

func groupMethods(reader *groupservice.Service) []Method {
	return []Method{
		method(groupservice.ListMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input groupservice.ListInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodPageResult(reader.List(ctx, input))
		}),
		method(groupservice.GetMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input groupservice.GetInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodResult(reader.Get(ctx, input))
		}),
		method(groupservice.SearchMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input groupservice.SearchInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodPageResult(reader.Search(ctx, input))
		}),
		method(groupservice.MembersListMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input groupservice.MembersInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodPageResult(reader.Members(ctx, input))
		}),
		method(groupservice.MembersSearchMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input groupservice.MembersSearchInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodPageResult(reader.SearchMembers(ctx, input))
		}),
	}
}

func socialMethods(reader *socialservice.Service) []Method {
	return []Method{
		method(socialservice.FriendListMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input socialservice.ListInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodPageResult(reader.Friends(ctx, input))
		}),
		method(socialservice.FriendGetMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input socialservice.GetInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodResult(reader.Friend(ctx, input))
		}),
		method(socialservice.FriendSearchMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input socialservice.SearchInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodPageResult(reader.SearchFriends(ctx, input))
		}),
		method(socialservice.BlackListMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input socialservice.ListInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodPageResult(reader.Blacklist(ctx, input))
		}),
		method(socialservice.BlackGetMethod, func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
			var input socialservice.GetInput
			if err := decodeParams(raw, &input); err != nil {
				return MethodResult{}, err
			}
			return methodResult(reader.Black(ctx, input))
		}),
	}
}

func method(name string, handle func(context.Context, json.RawMessage) (MethodResult, error)) Method {
	return Method{Name: name, Handle: handle}
}

func decodeParams(raw json.RawMessage, output any) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return MethodFailure(contracts.CodeInvalidArgument, "typed method parameters must be an object", false)
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return MethodFailure(contracts.CodeInvalidArgument, "invalid typed method parameters", false)
	}
	return nil
}

func methodResult[T any](value service.Result[T], err error) (MethodResult, error) {
	if err != nil {
		return MethodResult{}, serviceFailure(err)
	}
	return MethodResult{Data: value.Data, Meta: service.ContractMeta(value.Meta)}, nil
}

func methodPageResult[T any](value service.PageResult[T], err error) (MethodResult, error) {
	if err != nil {
		return MethodResult{}, serviceFailure(err)
	}
	return MethodResult{Data: value.Data, Meta: service.ContractMeta(value.Meta)}, nil
}

func serviceFailure(err error) error {
	switch {
	case errors.Is(err, service.ErrInvalidArgument), errors.Is(err, service.ErrCursorInvalid):
		return MethodFailure(contracts.CodeInvalidArgument, "invalid typed service parameters", false)
	case errors.Is(err, service.ErrCursorExpired):
		return MethodFailure(contracts.CodeCursorExpired, "typed service cursor has expired", false)
	case errors.Is(err, service.ErrTargetDenied):
		return MethodFailure(contracts.CodeInternal, "typed service failed", false)
	default:
		return MethodFailure(contracts.CodeInternal, "typed service failed", false)
	}
}
