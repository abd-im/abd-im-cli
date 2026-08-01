package connector

import (
	"errors"

	groupcapability "github.com/abd-im/abd-im-cli/internal/capability/group"
)

// GroupAdministrationActions exposes only fixed group-administration server
// actions and role checks from the daemon-owned adapter context. It does not
// expose SDK Group APIs or local synchronized group state.
func (p *Prepared) GroupAdministrationActions() (*groupcapability.OpenIMAdministrationSource, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return groupcapability.NewOpenIMAdministrationSource(groupcapability.OpenIMAdministrationSource{Context: p.Adapter.Context})
}
