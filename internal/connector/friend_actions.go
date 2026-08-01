package connector

import (
	"errors"

	friendcapability "github.com/abd-im/abd-im-cli/internal/capability/friend"
)

// FriendActions exposes only fixed friend lifecycle actions and state reads
// from the daemon-owned adapter context. It has no SDK local database API.
func (p *Prepared) FriendActions() (*friendcapability.OpenIMActions, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return friendcapability.NewOpenIMActions(friendcapability.OpenIMActions{Context: p.Adapter.Context})
}
