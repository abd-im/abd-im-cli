//go:build !unix

package provider

import "errors"

// Bridge is implemented with an owner-only Unix socket on the current P1
// target. The release launcher will provide the Windows transport separately.
type Bridge struct{}

func StartBridge(string, *Server) (*Bridge, error) {
	return nil, errors.New("provider MCP bridge requires a Unix socket")
}

func (*Bridge) Close() error { return nil }
