// Package friend implements verified friend-domain remote actions.
package friend

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

const (
	RequestMethod   = "friend.request"
	RespondMethod   = "friend.respond"
	DeleteMethod    = "friend.delete"
	SetRemarkMethod = "friend.set_remark"

	RequestScope   = RequestMethod
	RespondScope   = RespondMethod
	DeleteScope    = DeleteMethod
	SetRemarkScope = SetRemarkMethod

	maxUserIDBytes          = 256
	maxRequestMessageBytes  = 512
	maxResponseMessageBytes = 512
	maxRemarkBytes          = 128
)

var (
	errPendingRequestMissing = errors.New("friend request is not pending")
	errFriendMissing         = errors.New("friend relationship does not exist")
)

// RequestInput targets one user. The daemon derives the requesting owner
// from its authenticated SDK context rather than accepting it from a provider.
type RequestInput struct {
	UserID  string `json:"user_id"`
	Message string `json:"message,omitempty"`
}

// RespondInput accepts or rejects one pending incoming friend request.
type RespondInput struct {
	UserID   string `json:"user_id"`
	Response string `json:"response"`
	Message  string `json:"message,omitempty"`
}

// DeleteInput identifies one existing friend relationship to remove.
type DeleteInput struct {
	UserID string `json:"user_id"`
}

// SetRemarkInput updates only the user-visible friend remark. An empty remark
// clears the existing value; no generic friend patch is exposed.
type SetRemarkInput struct {
	UserID string `json:"user_id"`
	Remark string `json:"remark"`
}

// Source is the narrow daemon-owned server action and state verification
// surface. Implementations must not use SDK local friend tables.
type Source interface {
	RequestFriend(context.Context, RequestInput) error
	RespondFriend(context.Context, RespondInput) error
	DeleteFriend(context.Context, DeleteInput) error
	SetFriendRemark(context.Context, SetRemarkInput) error
	HasPendingRequest(context.Context, string) (bool, error)
	HasFriend(context.Context, string) (bool, error)
}

// Handler exposes the fixed friend lifecycle methods to one run-scoped proxy.
type Handler struct {
	guard  *operation.Guard
	source Source
}

func New(guard *operation.Guard, source Source) (*Handler, error) {
	if guard == nil || source == nil {
		return nil, errors.New("operation guard and friend source are required")
	}
	return &Handler{guard: guard, source: source}, nil
}

// ProxyMethods returns only the fixed friend methods.
func (h *Handler) ProxyMethods() []proxy.Method {
	return []proxy.Method{
		{Name: RequestMethod, Handle: h.request},
		{Name: RespondMethod, Handle: h.respond},
		{Name: DeleteMethod, Handle: h.delete},
		{Name: SetRemarkMethod, Handle: h.setRemark},
	}
}

func (h *Handler) request(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseRequest(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid friend.request input")
	}
	return h.execute(ctx, request, RequestMethod, RequestScope, input, func(ctx context.Context) error {
		return h.source.RequestFriend(ctx, input)
	})
}

func (h *Handler) respond(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseRespond(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid friend.respond input")
	}
	return h.execute(ctx, request, RespondMethod, RespondScope, input, func(ctx context.Context) error {
		pending, err := h.source.HasPendingRequest(ctx, input.UserID)
		if err != nil {
			return err
		}
		if !pending {
			return errPendingRequestMissing
		}
		return h.source.RespondFriend(ctx, input)
	})
}

func (h *Handler) delete(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseDelete(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid friend.delete input")
	}
	return h.execute(ctx, request, DeleteMethod, DeleteScope, input, func(ctx context.Context) error {
		exists, err := h.source.HasFriend(ctx, input.UserID)
		if err != nil {
			return err
		}
		if !exists {
			return errFriendMissing
		}
		return h.source.DeleteFriend(ctx, input)
	})
}

func (h *Handler) setRemark(ctx context.Context, request contracts.Request, _ grant.Grant) (json.RawMessage, error) {
	input, err := parseSetRemark(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid friend.set_remark input")
	}
	return h.execute(ctx, request, SetRemarkMethod, SetRemarkScope, input, func(ctx context.Context) error {
		return h.source.SetFriendRemark(ctx, input)
	})
}

func (h *Handler) execute(ctx context.Context, request contracts.Request, method, scope string, input any, effect operation.Effect) (json.RawMessage, error) {
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, method+" requires idempotency_key")
	}
	outcome, err := h.guard.Execute(ctx, operation.Request{
		ID:             strings.ReplaceAll(method, ".", "-") + "-" + request.IdempotencyKey,
		ProfileID:      request.ProfileID,
		Scope:          scope,
		IdempotencyKey: request.IdempotencyKey,
		Input:          input,
	}, effect)
	if err != nil {
		switch {
		case errors.Is(err, errPendingRequestMissing), errors.Is(err, errFriendMissing):
			return nil, proxy.Failure(contracts.CodePolicyDenied, "friend state does not authorize action")
		case errors.Is(err, operation.ErrIdempotencyConflict):
			return nil, proxy.Failure(contracts.CodeIdempotencyConflict, "idempotency key has different input")
		case errors.Is(err, operation.ErrOutcomeUnknown):
			return nil, proxy.Failure(contracts.CodeOutcomeUnknown, "prior "+method+" outcome is unknown")
		default:
			return nil, proxy.Failure(contracts.CodeSDKError, method+" failed")
		}
	}
	return json.Marshal(struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
	}{outcome.Operation.ID, string(outcome.Operation.Status)})
}

func parseRequest(raw json.RawMessage) (RequestInput, error) {
	var input RequestInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return RequestInput{}, err
	}
	input.UserID = strings.TrimSpace(input.UserID)
	input.Message = strings.TrimSpace(input.Message)
	if !validUserID(input.UserID) || len(input.Message) > maxRequestMessageBytes {
		return RequestInput{}, errors.New("friend request user ID or message is invalid")
	}
	return input, nil
}

func parseRespond(raw json.RawMessage) (RespondInput, error) {
	var input RespondInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return RespondInput{}, err
	}
	input.UserID = strings.TrimSpace(input.UserID)
	input.Response = strings.TrimSpace(input.Response)
	input.Message = strings.TrimSpace(input.Message)
	if !validUserID(input.UserID) || (input.Response != "accept" && input.Response != "reject") || len(input.Message) > maxResponseMessageBytes {
		return RespondInput{}, errors.New("friend response user ID, response, or message is invalid")
	}
	return input, nil
}

func parseDelete(raw json.RawMessage) (DeleteInput, error) {
	var input DeleteInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return DeleteInput{}, err
	}
	input.UserID = strings.TrimSpace(input.UserID)
	if !validUserID(input.UserID) {
		return DeleteInput{}, errors.New("friend user ID is invalid")
	}
	return input, nil
}

func parseSetRemark(raw json.RawMessage) (SetRemarkInput, error) {
	var input SetRemarkInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return SetRemarkInput{}, err
	}
	input.UserID = strings.TrimSpace(input.UserID)
	input.Remark = strings.TrimSpace(input.Remark)
	if !validUserID(input.UserID) || len(input.Remark) > maxRemarkBytes {
		return SetRemarkInput{}, errors.New("friend remark user ID or remark is invalid")
	}
	return input, nil
}

func validUserID(userID string) bool {
	return userID != "" && len(userID) <= maxUserIDBytes
}

var _ Source = (*OpenIMActions)(nil)
