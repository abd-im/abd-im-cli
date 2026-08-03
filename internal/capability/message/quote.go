package message

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/capability"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

const (
	QuoteMethod = "message.send_quote"
	QuoteScope  = "message.send_quote"
)

var errQuoteOutsideWindow = errors.New("quoted message is outside the grant window")

// QuoteInput identifies the bounded source message and one outbound target.
type QuoteInput struct {
	Text           string `json:"text"`
	RecipientID    string `json:"recipient_id,omitempty"`
	GroupID        string `json:"group_id,omitempty"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
}

// QuoteReference is the minimum server-read fact required to prove that a
// quoted message belongs to a grant's message window.
type QuoteReference struct {
	ID             string
	ConversationID string
}

// QuoteSource provides only the fixed server-read history required to verify
// a quote reference. It does not expose SDK or local database APIs.
type QuoteSource interface {
	History(context.Context, string) ([]QuoteReference, error)
}

// QuoteSender delivers one already-authorized quote through the daemon-owned
// SDK adapter. The adapter resolves the opaque source message itself.
type QuoteSender interface {
	SendQuote(context.Context, QuoteInput) error
}

type QuoteHandler struct {
	manifest *capability.Manifest
	guard    *operation.Guard
	source   QuoteSource
	sender   QuoteSender
}

func NewQuote(manifest *capability.Manifest, guard *operation.Guard, source QuoteSource, sender QuoteSender) (*QuoteHandler, error) {
	if manifest == nil || guard == nil || source == nil || sender == nil {
		return nil, errors.New("manifest, operation guard, quote source, and quote sender are required")
	}
	return &QuoteHandler{manifest: manifest, guard: guard, source: source, sender: sender}, nil
}

func (h *QuoteHandler) ProxyMethod() proxy.Method {
	return proxy.Method{
		Name:    QuoteMethod,
		Scope:   QuoteScope,
		Allowed: func() bool { return h.manifest.Allows(QuoteMethod, QuoteScope) },
		Targets: quoteTargets,
		Handle:  h.handle,
	}
}

func quoteTargets(raw json.RawMessage) ([]string, error) {
	input, err := parseQuote(raw)
	if err != nil {
		return nil, err
	}
	targets := []string{grant.ConversationTarget(input.ConversationID), grant.MessageTarget(input.MessageID)}
	if input.RecipientID != "" {
		return append(targets, grant.UserTarget(input.RecipientID)), nil
	}
	return append(targets, grant.GroupTarget(input.GroupID)), nil
}

func (h *QuoteHandler) handle(ctx context.Context, request contracts.Request, item grant.Grant) (json.RawMessage, error) {
	input, err := parseQuote(request.Params)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid message.send_quote input")
	}
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "message.send_quote requires idempotency_key")
	}
	outcome, err := h.guard.Execute(ctx, operation.Request{
		ID:             "message-quote-" + request.IdempotencyKey,
		ProfileID:      request.ProfileID,
		Scope:          QuoteScope,
		IdempotencyKey: request.IdempotencyKey,
		Input:          input,
	}, func(ctx context.Context) error {
		if err := h.verifyQuote(ctx, item.MessageWindow, input); err != nil {
			return err
		}
		return h.sender.SendQuote(ctx, input)
	})
	if err != nil {
		if errors.Is(err, errQuoteOutsideWindow) {
			return nil, proxy.Failure(contracts.CodePolicyDenied, "quoted message is outside the grant window")
		}
		if errors.Is(err, operation.ErrIdempotencyConflict) {
			return nil, proxy.Failure(contracts.CodeIdempotencyConflict, "idempotency key has different input")
		}
		if errors.Is(err, operation.ErrOutcomeUnknown) {
			return nil, proxy.Failure(contracts.CodeOutcomeUnknown, "prior message.send_quote outcome is unknown")
		}
		return nil, proxy.Failure(contracts.CodeSDKError, "message.send_quote failed")
	}
	return json.Marshal(struct {
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
	}{outcome.Operation.ID, string(outcome.Operation.Status)})
}

func (h *QuoteHandler) verifyQuote(ctx context.Context, window grant.MessageWindow, input QuoteInput) error {
	if window.ConversationID == "" || window.ConversationID != input.ConversationID {
		return errQuoteOutsideWindow
	}
	references, err := h.source.History(ctx, input.ConversationID)
	if err != nil {
		return fmt.Errorf("read quote source: %w", err)
	}
	for _, reference := range references {
		if reference.ID == "" || reference.ConversationID != input.ConversationID {
			return errors.New("quote source returned an invalid reference")
		}
	}
	start, end := 0, len(references)
	start, end, err = quoteWindow(references, window)
	if err != nil {
		return err
	}
	for _, reference := range references[start:end] {
		if reference.ID == input.MessageID {
			return nil
		}
	}
	return errQuoteOutsideWindow
}

func quoteWindow(references []QuoteReference, window grant.MessageWindow) (int, int, error) {
	start, end := 0, len(references)
	if window.AfterMessageID != "" {
		found := false
		for index, reference := range references {
			if reference.ID == window.AfterMessageID {
				start, found = index+1, true
				break
			}
		}
		if !found {
			return 0, 0, errQuoteOutsideWindow
		}
	}
	if window.BeforeMessageID != "" {
		found := false
		for index, reference := range references {
			if reference.ID == window.BeforeMessageID {
				end, found = index, true
				break
			}
		}
		if !found || start > end {
			return 0, 0, errQuoteOutsideWindow
		}
	}
	return start, end, nil
}

func parseQuote(raw json.RawMessage) (QuoteInput, error) {
	var input QuoteInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return QuoteInput{}, err
	}
	if strings.TrimSpace(input.Text) == "" || len(input.Text) > maxTextBytes {
		return QuoteInput{}, errors.New("message text must contain 1-4096 bytes")
	}
	input.RecipientID = strings.TrimSpace(input.RecipientID)
	input.GroupID = strings.TrimSpace(input.GroupID)
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	input.MessageID = strings.TrimSpace(input.MessageID)
	if (input.RecipientID == "" && input.GroupID == "") || (input.RecipientID != "" && input.GroupID != "") {
		return QuoteInput{}, errors.New("exactly one message recipient is required")
	}
	if input.ConversationID == "" || input.MessageID == "" {
		return QuoteInput{}, errors.New("conversation ID and message ID are required")
	}
	return input, nil
}
