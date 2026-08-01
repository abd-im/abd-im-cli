package connector

import (
	"errors"

	conversationcapability "github.com/abd-im/abd-im-cli/internal/capability/conversation"
	conversationservice "github.com/abd-im/abd-im-cli/internal/service/conversation"
)

// ConversationSettings exposes only the verified server-read and fixed
// server-write conversation setting source. It does not expose SDK local
// conversation APIs.
func (p *Prepared) ConversationSettings() (*conversationcapability.OpenIMSettings, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return conversationcapability.NewOpenIMSettings(conversationcapability.OpenIMSettings{
		Context: p.Adapter.Context,
		Client:  conversationservice.OpenIMClient{Context: p.Adapter.Context},
	})
}
