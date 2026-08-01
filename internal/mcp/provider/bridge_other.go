//go:build !unix

package provider

import "errors"

// Bridge is implemented with an owner-only Unix socket on the current P1
// target. Windows provider transport is outside the current release target.
type Bridge struct{}

func StartBridge(string, *Server) (*Bridge, error) {
	return nil, errors.New("provider MCP bridge requires a Unix socket")
}

func (*Bridge) Close() error { return nil }
