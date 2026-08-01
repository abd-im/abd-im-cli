package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/operation"
	conversationservice "github.com/abd-im/abd-im-cli/internal/service/conversation"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	pbconstant "github.com/openimsdk/protocol/constant"
	pbconversation "github.com/openimsdk/protocol/conversation"
)

func TestOpenIMSettingsVerifyThenPostOnlyTypedSetting(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		if request.Header.Get("token") != "secret-token" || request.Header.Get("operationID") == "" {
			t.Fatalf("request lacks authentication headers")
		}
		switch request.URL.Path {
		case "/conversation/get_conversations":
			var input pbconversation.GetConversationsReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.OwnerUserID != "user-1" || !reflect.DeepEqual(input.ConversationIDs, []string{"conversation-1"}) {
				t.Fatalf("verification owner=%q conversations=%v", input.OwnerUserID, input.ConversationIDs)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0, "data": &pbconversation.GetConversationsResp{Conversations: []*pbconversation.Conversation{{ConversationID: "conversation-1", ConversationType: pbconstant.SingleChatType, UserID: "user-2"}}}})
		case "/conversation/set_conversations":
			var input pbconversation.SetConversationsReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(input.UserIDs, []string{"user-1"}) || input.Conversation == nil || input.Conversation.ConversationID != "conversation-1" || input.Conversation.IsPinned == nil || !input.Conversation.IsPinned.Value || input.Conversation.RecvMsgOpt != nil || input.Conversation.AttachedInfo != nil || input.Conversation.Ex != nil || input.Conversation.IsPrivateChat != nil || input.Conversation.BurnDuration != nil {
				t.Fatalf("pinned action exposed a generic patch: %+v", input.Conversation)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0})
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	settings, err := newOpenIMSettingsForTest(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.SetPinned(context.Background(), SetPinnedInput{ConversationID: "conversation-1", Pinned: true}); err != nil {
		t.Fatalf("SetPinned() error = %v", err)
	}
	if want := []string{"/conversation/get_conversations", "/conversation/set_conversations"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestOpenIMSettingsPostsFixedReceiveOption(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/conversation/get_conversations":
			_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0, "data": &pbconversation.GetConversationsResp{Conversations: []*pbconversation.Conversation{{ConversationID: "conversation-1", ConversationType: pbconstant.SingleChatType, UserID: "user-2"}}}})
		case "/conversation/set_conversations":
			var input pbconversation.SetConversationsReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.Conversation == nil || input.Conversation.ConversationID != "conversation-1" || input.Conversation.RecvMsgOpt == nil || input.Conversation.RecvMsgOpt.Value != pbconstant.ReceiveNotNotifyMessage || input.Conversation.IsPinned != nil {
				t.Fatalf("receive option input = %+v", input.Conversation)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0})
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	settings, err := newOpenIMSettingsForTest(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.SetReceiveOption(context.Background(), SetReceiveOptionInput{ConversationID: "conversation-1", Option: ReceiveOptionReceiveNoNotify}); err != nil {
		t.Fatalf("SetReceiveOption() error = %v", err)
	}
}

func TestOpenIMSettingsRejectsMissingConversationAndPreservesUnknownOutcome(t *testing.T) {
	missingClient := &fakeSettingsClient{}
	settings, err := NewOpenIMSettings(OpenIMSettings{Context: settingsTestContext("https://im.example.test"), Client: missingClient})
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.SetPinned(context.Background(), SetPinnedInput{ConversationID: "missing", Pinned: true}); err == nil {
		t.Fatal("SetPinned() accepted a nonexistent server conversation")
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/conversation/get_conversations":
			_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0, "data": &pbconversation.GetConversationsResp{Conversations: []*pbconversation.Conversation{{ConversationID: "conversation-1", ConversationType: pbconstant.SingleChatType, UserID: "user-2"}}}})
		case "/conversation/set_conversations":
			writer.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer server.Close()
	settings, err = newOpenIMSettingsForTest(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.SetPinned(context.Background(), SetPinnedInput{ConversationID: "conversation-1", Pinned: true}); !errors.Is(err, operation.ErrOutcomeUnknown) {
		t.Fatalf("SetPinned() error = %v, want unknown outcome", err)
	}
}

func TestNewOpenIMSettingsRequiresServerReadClient(t *testing.T) {
	if _, err := NewOpenIMSettings(OpenIMSettings{}); err == nil {
		t.Fatal("NewOpenIMSettings() error = nil")
	}
}

func newOpenIMSettingsForTest(apiAddr string, client *http.Client) (*OpenIMSettings, error) {
	context := settingsTestContext(apiAddr)
	serverClient := conversationservice.OpenIMClient{Context: context, HTTPClient: client}
	return NewOpenIMSettings(OpenIMSettings{Context: context, Client: serverClient, HTTPClient: client})
}

func settingsTestContext(apiAddr string) func() context.Context {
	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID:   "user-1",
		Token:    "secret-token",
		IMConfig: &sdk_struct.IMConfig{ApiAddr: apiAddr},
	})
	return func() context.Context { return base }
}

type fakeSettingsClient struct {
	items []*pbconversation.Conversation
	err   error
}

func (c *fakeSettingsClient) AllConversations(context.Context) ([]*pbconversation.Conversation, error) {
	return c.items, c.err
}

func (c *fakeSettingsClient) Conversations(context.Context, []string) ([]*pbconversation.Conversation, error) {
	return c.items, c.err
}

var _ conversationservice.Client = (*fakeSettingsClient)(nil)

func TestOpenIMSettingsRedactsServerRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/conversation/get_conversations":
			_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0, "data": &pbconversation.GetConversationsResp{Conversations: []*pbconversation.Conversation{{ConversationID: "conversation-1", ConversationType: pbconstant.SingleChatType, UserID: "user-2"}}}})
		case "/conversation/set_conversations":
			_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 1001, "errMsg": "secret-token"})
		}
	}))
	defer server.Close()
	settings, err := newOpenIMSettingsForTest(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = settings.SetReceiveOption(context.Background(), SetReceiveOptionInput{ConversationID: "conversation-1", Option: ReceiveOptionReceive})
	if err == nil || errors.Is(err, operation.ErrOutcomeUnknown) || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("SetReceiveOption() rejection = %v", err)
	}
}
