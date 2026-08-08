package group

import (
	"context"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/openimsdk/protocol/sdkws"
)

func TestConversationKindFromGroupEx(t *testing.T) {
	tests := []struct {
		name string
		ex   string
		want contracts.ConversationKind
	}{
		{name: "missing", want: contracts.ConversationKindChat},
		{name: "invalid", ex: `{`, want: contracts.ConversationKindChat},
		{name: "unknown", ex: `{"abd":{"kind":"other","version":1}}`, want: contracts.ConversationKindChat},
		{name: "workspace", ex: `{"abd":{"kind":"agent_workspace","version":1}}`, want: contracts.ConversationKindAgentWorkspace},
		{name: "unrelated fields", ex: `{"other":{"value":1},"abd":{"kind":"agent_workspace","version":1}}`, want: contracts.ConversationKindAgentWorkspace},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ConversationKindFromGroupEx(test.ex); got != test.want {
				t.Fatalf("ConversationKindFromGroupEx() = %q, want %q", got, test.want)
			}
		})
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
	if len(client.groupIDs) != 1 || client.groupIDs[0] != "group-1" {
		t.Fatalf("queried group IDs = %v", client.groupIDs)
	}
}
