package message

import (
	"context"
	"errors"
	"sort"
	"strings"

	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/converter"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/utils"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	"github.com/openimsdk/protocol/sdkws"
)

const quoteHistoryLimit = 100

// OpenIMQuoteSender is the narrow daemon-only quote delivery surface. The
// quoted SDK message is obtained only from the fixed server-read client.
type OpenIMQuoteSender interface {
	SendQuote(context.Context, string, string, string, *sdk_struct.MsgStruct) error
}

// OpenIMQuoteSource verifies quote references from server messages and sends
// only the exact matching server message through the daemon-owned adapter.
// It has no SDK database access.
type OpenIMQuoteSource struct {
	client messageservice.Client
	sender OpenIMQuoteSender
	selfID string
}

func NewOpenIMQuoteSource(client messageservice.Client, sender OpenIMQuoteSender, selfID string) (*OpenIMQuoteSource, error) {
	if client == nil || sender == nil || strings.TrimSpace(selfID) == "" {
		return nil, errors.New("OpenIM quote client, sender, and self ID are required")
	}
	return &OpenIMQuoteSource{client: client, sender: sender, selfID: selfID}, nil
}

func (s *OpenIMQuoteSource) History(ctx context.Context, conversationID string) ([]QuoteReference, error) {
	items, err := s.client.Messages(ctx, conversationID, quoteHistoryLimit)
	if err != nil {
		return nil, err
	}
	references := make([]QuoteReference, 0, len(items))
	for _, item := range items {
		reference, err := quoteReference(item, conversationID)
		if err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	return references, nil
}

func (s *OpenIMQuoteSource) SendQuote(ctx context.Context, input QuoteInput) error {
	items, err := s.client.Messages(ctx, input.ConversationID, quoteHistoryLimit)
	if err != nil {
		return err
	}
	for _, item := range items {
		reference, err := quoteReference(item, input.ConversationID)
		if err != nil {
			return err
		}
		if reference.ID != input.MessageID {
			continue
		}
		if !s.matchesTarget(input, item) {
			return errors.New("quoted message target does not match input")
		}
		quoted := converter.MsgDataToMsgStruct(item)
		if quoted == nil {
			return errors.New("quoted server message is invalid")
		}
		return s.sender.SendQuote(ctx, input.Text, input.RecipientID, input.GroupID, quoted)
	}
	return errors.New("quoted server message was not found")
}

func (s *OpenIMQuoteSource) matchesTarget(input QuoteInput, item *sdkws.MsgData) bool {
	if input.RecipientID != "" {
		return input.ConversationID == directConversationID(s.selfID, input.RecipientID)
	}
	return item != nil && item.GroupID == input.GroupID
}

func quoteReference(item *sdkws.MsgData, conversationID string) (QuoteReference, error) {
	quoted := converter.MsgDataToMsgStruct(item)
	if quoted == nil || utils.GetConversationIDByMsg(quoted) != conversationID {
		return QuoteReference{}, errors.New("OpenIM quote source returned an unexpected conversation")
	}
	id := strings.TrimSpace(item.ServerMsgID)
	if id == "" {
		id = strings.TrimSpace(item.ClientMsgID)
	}
	if id == "" {
		return QuoteReference{}, errors.New("OpenIM quote source message has no ID")
	}
	return QuoteReference{ID: id, ConversationID: conversationID}, nil
}

func directConversationID(first, second string) string {
	ids := []string{first, second}
	sort.Strings(ids)
	return "si_" + strings.Join(ids, "_")
}

var _ QuoteSource = (*OpenIMQuoteSource)(nil)
var _ QuoteSender = (*OpenIMQuoteSource)(nil)
