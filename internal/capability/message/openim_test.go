package message

import (
	"context"
	"errors"
	"reflect"
	"testing"

	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	pbconstant "github.com/openimsdk/protocol/constant"
	"github.com/openimsdk/protocol/sdkws"
)

func TestOpenIMQuoteSourceUsesFixedServerHistoryAndExactMessage(t *testing.T) {
	client := &fakeQuoteOpenIMClient{messages: []*sdkws.MsgData{
		{ServerMsgID: "message-1", ClientMsgID: "client-1", SendID: "user-2", RecvID: "user-1", SessionType: pbconstant.SingleChatType, Seq: 11},
		{ServerMsgID: "message-2", ClientMsgID: "client-2", SendID: "user-1", RecvID: "user-2", SessionType: pbconstant.SingleChatType, Seq: 12},
	}}
	sender := &fakeOpenIMQuoteSender{}
	source, err := NewOpenIMQuoteSource(client, sender, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	conversationID := "si_user-1_user-2"
	history, err := source.History(context.Background(), conversationID)
	if err != nil || !reflect.DeepEqual(history, []QuoteReference{{ID: "message-1", ConversationID: conversationID}, {ID: "message-2", ConversationID: conversationID}}) || client.limit != quoteHistoryLimit {
		t.Fatalf("History() = %#v, %v; limit=%d", history, err, client.limit)
	}
	input := QuoteInput{Text: "reply", RecipientID: "user-2", ConversationID: conversationID, MessageID: "message-1"}
	if err := source.SendQuote(context.Background(), input); err != nil {
		t.Fatalf("SendQuote() error = %v", err)
	}
	if sender.calls != 1 || sender.text != "reply" || sender.recipientID != "user-2" || sender.groupID != "" || sender.quoted == nil || sender.quoted.ServerMsgID != "message-1" || sender.quoted.Seq != 11 {
		t.Fatalf("quoted sender = %+v", sender)
	}
}

func TestOpenIMQuoteSourceFailsClosedForUnexpectedMessageOrTarget(t *testing.T) {
	client := &fakeQuoteOpenIMClient{messages: []*sdkws.MsgData{{
		ServerMsgID: "message-1", SendID: "user-2", RecvID: "user-1", SessionType: pbconstant.SingleChatType,
	}}}
	sender := &fakeOpenIMQuoteSender{}
	source, err := NewOpenIMQuoteSource(client, sender, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := source.SendQuote(context.Background(), QuoteInput{Text: "reply", RecipientID: "user-3", ConversationID: "si_user-1_user-2", MessageID: "message-1"}); err == nil || sender.calls != 0 {
		t.Fatalf("mismatched direct target = %v, calls=%d", err, sender.calls)
	}
	client.messages[0].RecvID = "user-3"
	if _, err := source.History(context.Background(), "si_user-1_user-2"); err == nil {
		t.Fatal("History() accepted a message from another conversation")
	}
	if _, err := NewOpenIMQuoteSource(nil, sender, "user-1"); err == nil {
		t.Fatal("NewOpenIMQuoteSource() accepted nil client")
	}
}

type fakeQuoteOpenIMClient struct {
	messages       []*sdkws.MsgData
	conversationID string
	limit          int
	err            error
}

func (c *fakeQuoteOpenIMClient) Messages(_ context.Context, conversationID string, limit int) ([]*sdkws.MsgData, error) {
	c.conversationID = conversationID
	c.limit = limit
	if c.err != nil {
		return nil, c.err
	}
	return c.messages, nil
}

type fakeOpenIMQuoteSender struct {
	calls       int
	text        string
	recipientID string
	groupID     string
	quoted      *sdk_struct.MsgStruct
	err         error
}

func (s *fakeOpenIMQuoteSender) SendQuote(_ context.Context, text, recipientID, groupID string, quoted *sdk_struct.MsgStruct) error {
	s.calls++
	s.text = text
	s.recipientID = recipientID
	s.groupID = groupID
	s.quoted = quoted
	return s.err
}

var _ messageservice.Client = (*fakeQuoteOpenIMClient)(nil)
var _ OpenIMQuoteSender = (*fakeOpenIMQuoteSender)(nil)

func TestOpenIMQuoteSourceReturnsClientFailure(t *testing.T) {
	client := &fakeQuoteOpenIMClient{err: errors.New("server unavailable")}
	source, err := NewOpenIMQuoteSource(client, &fakeOpenIMQuoteSender{}, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.History(context.Background(), "si_user-1_user-2"); err == nil {
		t.Fatal("History() error = nil")
	}
}
