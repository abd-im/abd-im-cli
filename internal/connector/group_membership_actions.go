package connector

import (
	"errors"

	groupcapability "github.com/abd-im/abd-im-cli/internal/capability/group"
)

// GroupMembershipActions exposes only fixed group membership server actions
// and checks from the daemon-owned adapter context. It has no SDK local group
// state API.
func (p *Prepared) GroupMembershipActions() (*groupcapability.OpenIMMembershipSource, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return groupcapability.NewOpenIMMembershipSource(groupcapability.OpenIMMembershipSource{Context: p.Adapter.Context})
}
