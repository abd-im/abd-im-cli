package group

import (
	"context"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/openimsdk/protocol/sdkws"
)

func TestConversationKindFromGroupEx(t *testing.T) {
	tests := []struct {
		ex   string
		want contracts.ConversationKind
	}{
		{want: contracts.ConversationKindChat},
		{ex: `{`, want: contracts.ConversationKindChat},
		{ex: `{"abd":{"kind":"other","version":1}}`, want: contracts.ConversationKindChat},
		{ex: `{"abd":{"kind":"agent_workspace","version":1}}`, want: contracts.ConversationKindAgentWorkspace},
	}
	for _, test := range tests {
		if got := ConversationKindFromGroupEx(test.ex); got != test.want {
			t.Fatalf("ConversationKindFromGroupEx(%q) = %q, want %q", test.ex, got, test.want)
		}
	}
}

func TestSDKSourceConversationKind(t *testing.T) {
	client := &fakeOpenIMClient{groups: []*sdkws.GroupInfo{{GroupID: "group-1", Ex: `{"abd":{"kind":"agent_workspace","version":1}}`}}}
	source, err := NewSDKSource(client)
	if err != nil {
		t.Fatal(err)
	}
	kind, err := source.ConversationKind(context.Background(), "group-1")
	if err != nil || kind != contracts.ConversationKindAgentWorkspace {
		t.Fatalf("ConversationKind() = %q, %v", kind, err)
	}
}
