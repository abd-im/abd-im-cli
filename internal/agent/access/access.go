// Package access exposes one run-scoped local RPC endpoint to an Agent process.
package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/ipc"
)

const (
	EnvSocket  = "ABDIM_AGENT_SOCKET"
	EnvProfile = "ABDIM_AGENT_PROFILE"
	EnvRun     = "ABDIM_AGENT_RUN"
	EnvGrant   = "ABDIM_AGENT_GRANT"
	EnvMethods = "ABDIM_AGENT_METHODS"
	EnvCLI     = "ABDIM_CLI"
)

// Context is the complete daemon-issued CLI context for one Agent run.
type Context struct {
	Socket         string
	ProfileID      string
	RunID          string
	Grant          string
	AllowedMethods []string
}

// FromEnvironment returns the Agent context when any run marker is present.
// Partial contexts fail closed so a provider cannot fall back to owner access.
func FromEnvironment(lookup func(string) string) (Context, bool, error) {
	if lookup == nil {
		lookup = os.Getenv
	}
	values := Context{
		Socket:    strings.TrimSpace(lookup(EnvSocket)),
		ProfileID: strings.TrimSpace(lookup(EnvProfile)),
		RunID:     strings.TrimSpace(lookup(EnvRun)),
		Grant:     strings.TrimSpace(lookup(EnvGrant)),
	}
	rawMethods := strings.TrimSpace(lookup(EnvMethods))
	present := values.Socket != "" || values.ProfileID != "" || values.RunID != "" || values.Grant != "" || rawMethods != ""
	if !present {
		return Context{}, false, nil
	}
	if !filepath.IsAbs(values.Socket) || values.ProfileID == "" || values.RunID == "" || values.Grant == "" || rawMethods == "" {
		return Context{}, true, errors.New("incomplete abdim Agent access context")
	}
	if err := json.Unmarshal([]byte(rawMethods), &values.AllowedMethods); err != nil || values.AllowedMethods == nil {
		return Context{}, true, errors.New("invalid abdim Agent method context")
	}
	seen := make(map[string]struct{}, len(values.AllowedMethods))
	for _, method := range values.AllowedMethods {
		if strings.TrimSpace(method) == "" {
			return Context{}, true, errors.New("invalid abdim Agent method context")
		}
		if _, exists := seen[method]; exists {
			return Context{}, true, errors.New("duplicate abdim Agent method context")
		}
		seen[method] = struct{}{}
	}
	return values, true, nil
}

// Environment adds only the run-scoped values needed by the abdim CLI.
func Environment(base []string, cliPath string, values Context) ([]string, error) {
	if !filepath.IsAbs(cliPath) || !filepath.IsAbs(values.Socket) || values.ProfileID == "" || values.RunID == "" || values.Grant == "" {
		return nil, errors.New("complete abdim Agent access configuration is required")
	}
	allowedMethods := values.AllowedMethods
	if allowedMethods == nil {
		allowedMethods = []string{}
	}
	methods, err := json.Marshal(allowedMethods)
	if err != nil {
		return nil, errors.New("encode abdim Agent methods")
	}
	pathValue := filepath.Dir(cliPath)
	for _, value := range base {
		if strings.HasPrefix(value, "PATH=") {
			if inherited := strings.TrimPrefix(value, "PATH="); inherited != "" {
				pathValue += string(os.PathListSeparator) + inherited
			}
			break
		}
	}
	overrides := map[string]string{
		"PATH":     pathValue,
		EnvCLI:     cliPath,
		EnvSocket:  values.Socket,
		EnvProfile: values.ProfileID,
		EnvRun:     values.RunID,
		EnvGrant:   values.Grant,
		EnvMethods: string(methods),
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, value := range base {
		key, _, found := strings.Cut(value, "=")
		if found {
			if _, replaced := overrides[key]; replaced {
				continue
			}
		}
		result = append(result, value)
	}
	for _, key := range []string{"PATH", EnvCLI, EnvSocket, EnvProfile, EnvRun, EnvGrant, EnvMethods} {
		result = append(result, key+"="+overrides[key])
	}
	return result, nil
}

// Server owns the run-private socket used by abdim child processes.
type Server struct {
	server *ipc.Server
	cancel context.CancelFunc
	done   chan error
	close  sync.Once
	err    error
}

// Listen starts a local RPC server backed by the run's grant-enforcing proxy.
func Listen(socket string, proxy contracts.ToolProxy) (*Server, error) {
	if proxy == nil {
		return nil, errors.New("run tool proxy is required")
	}
	server, err := ipc.Listen(socket, proxy.Call)
	if err != nil {
		return nil, fmt.Errorf("listen for abdim Agent CLI: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	access := &Server{server: server, cancel: cancel, done: make(chan error, 1)}
	go func() { access.done <- server.Serve(ctx) }()
	return access, nil
}

// Close removes the run-private socket and waits for the server to stop.
func (s *Server) Close() error {
	if s == nil || s.server == nil {
		return nil
	}
	s.close.Do(func() {
		s.cancel()
		_ = s.server.Close()
		s.err = <-s.done
	})
	return s.err
}
