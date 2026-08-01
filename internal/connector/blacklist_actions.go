package connector

import (
	"errors"

	blacklistcapability "github.com/abd-im/abd-im-cli/internal/capability/blacklist"
)

// BlacklistActions exposes only fixed blacklist server actions from the
// daemon-owned adapter context. It does not expose SDK local relation state.
func (p *Prepared) BlacklistActions() (*blacklistcapability.OpenIMSource, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return blacklistcapability.NewOpenIMSource(blacklistcapability.OpenIMSource{Context: p.Adapter.Context})
}
