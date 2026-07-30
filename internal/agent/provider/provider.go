// Package provider owns the configured single provider adapter boundary.
package provider

import (
	"context"
	"errors"

	"github.com/abd-im-cli/abdim-cli/internal/contracts"
)

// Adapter wraps exactly one configured provider implementation. The daemon
// receives no endpoint override or arbitrary process command from this type.
type Adapter struct {
	provider contracts.Provider
}

func New(single contracts.Provider) (*Adapter, error) {
	if single == nil {
		return nil, errors.New("configured provider is required")
	}
	return &Adapter{provider: single}, nil
}

func (a *Adapter) Start(ctx context.Context, request contracts.StartRequest) (contracts.Session, error) {
	return a.provider.Start(ctx, request)
}

var _ contracts.Provider = (*Adapter)(nil)
