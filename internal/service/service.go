// Package service contains the shared contracts used by daemon-owned typed
// read services. Services depend on narrow SDK facades, never on SDK storage.
package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/abd-im-cli/abdim-cli/internal/agent/grant"
	"github.com/abd-im-cli/abdim-cli/internal/capability"
	"github.com/abd-im-cli/abdim-cli/internal/contracts"
)

const SchemaVersion = "abdim.service/v1"

var (
	ErrInvalidArgument       = errors.New("invalid service argument")
	ErrCapabilityUnavailable = errors.New("capability is unavailable")
	ErrScopeDenied           = errors.New("service scope is denied")
	ErrTargetDenied          = errors.New("service target is denied")
	ErrCursorInvalid         = errors.New("invalid cursor")
	ErrCursorExpired         = errors.New("cursor expired")
)

// Capability describes the public verification state of one typed method.
// The fields are deliberately descriptive so owner diagnostics can explain
// why a method is not exposed to a provider.
type Capability struct {
	Method        string `json:"method"`
	Scope         string `json:"scope"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	SDKVersion    string `json:"sdk_version,omitempty"`
	ServerVersion string `json:"server_version,omitempty"`
}

// Meta is attached to every successful typed response.
type Meta struct {
	ProfileID  string     `json:"profile_id"`
	Schema     string     `json:"schema"`
	Stale      bool       `json:"stale"`
	Capability Capability `json:"capability"`
}

// Result is the typed in-process equivalent of the versioned RPC envelope.
// The daemon adapter can serialize Data and Meta into contracts.Response.
type Result[T any] struct {
	Data T    `json:"data"`
	Meta Meta `json:"meta"`
}

type PageResult[T any] struct {
	Data Page[T] `json:"data"`
	Meta Meta    `json:"meta"`
}

func NewMeta(profileID string, stale bool, capability Capability) Meta {
	return Meta{ProfileID: profileID, Schema: SchemaVersion, Stale: stale, Capability: capability}
}

// ContractMeta converts a typed service result's metadata into the shared RPC
// envelope metadata without importing a service package into the transport.
func ContractMeta(meta Meta) contracts.Meta {
	return contracts.Meta{
		ProfileID: meta.ProfileID,
		Stale:     meta.Stale,
		Schema:    meta.Schema,
		Capability: &contracts.Capability{
			Method: meta.Capability.Method, Scope: meta.Capability.Scope,
			Status: meta.Capability.Status, Reason: meta.Capability.Reason,
			SDKVersion: meta.Capability.SDKVersion, ServerVersion: meta.Capability.ServerVersion,
		},
	}
}

// Page is the common list result. NextCursor is opaque and empty at the end.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// Item wraps a single typed value while preserving the same metadata shape as
// list responses.
type Item[T any] struct {
	Item T `json:"item"`
}

// Access captures the caller's authorization context. Owner callers bypass
// run grants; provider callers must supply a grant issued for the run.
type Access struct {
	Owner      bool
	Grant      grant.Grant
	Capability Capability
}

func OwnerAccess(capability Capability) Access {
	return Access{Owner: true, Capability: capability}
}

func ProviderAccess(item grant.Grant, capability Capability) Access {
	return Access{Grant: item, Capability: capability}
}

// Authorize validates the capability and, for provider calls, the grant's
// method, scope and target allowlist.
func (a Access) Authorize(method, scope string, targets ...string) error {
	if a.Capability.Method != "" && a.Capability.Method != method {
		return fmt.Errorf("%w: method %q", ErrCapabilityUnavailable, method)
	}
	if a.Capability.Scope != "" && a.Capability.Scope != scope {
		return fmt.Errorf("%w: scope %q", ErrCapabilityUnavailable, scope)
	}
	if a.Capability.Status != "" && a.Capability.Status != string(capability.Available) {
		return fmt.Errorf("%w: %s", ErrCapabilityUnavailable, a.Capability.Status)
	}
	if a.Owner {
		return nil
	}
	if !a.Grant.AllowsMethod(method) {
		return fmt.Errorf("%w: method %q", ErrScopeDenied, method)
	}
	if !a.Grant.AllowsScope(scope) {
		return fmt.Errorf("%w: scope %q", ErrScopeDenied, scope)
	}
	for _, target := range targets {
		if !a.Grant.AllowsTarget(target) {
			return fmt.Errorf("%w: target %q", ErrTargetDenied, target)
		}
	}
	return nil
}

// Cursor is encoded as an opaque, query-bound value. Services should reject a
// cursor created for a different query rather than silently changing pages.
type Cursor struct {
	Version uint8  `json:"v"`
	Query   string `json:"q"`
	Offset  int    `json:"o"`
}

func EncodeCursor(query string, offset int) (string, error) {
	if strings.TrimSpace(query) == "" || offset < 0 {
		return "", fmt.Errorf("%w: cursor input", ErrInvalidArgument)
	}
	payload, err := json.Marshal(Cursor{Version: 1, Query: queryDigest(query), Offset: offset})
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func DecodeCursor(value, query string) (int, error) {
	if value == "" {
		return 0, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, fmt.Errorf("%w: malformed cursor", ErrCursorInvalid)
	}
	var cursor Cursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Version != 1 || cursor.Offset < 0 {
		return 0, fmt.Errorf("%w: malformed cursor", ErrCursorInvalid)
	}
	if cursor.Query != queryDigest(query) {
		return 0, fmt.Errorf("%w: query changed", ErrCursorInvalid)
	}
	return cursor.Offset, nil
}

func queryDigest(query string) string {
	digest := sha256.Sum256([]byte(query))
	return base64.RawURLEncoding.EncodeToString(digest[:12])
}

// ValidateLimit applies the protocol-wide list bound.
func ValidateLimit(limit int) error {
	if limit <= 0 || limit > 100 {
		return fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidArgument)
	}
	return nil
}

// CapabilityFromManifest translates the existing capability registry into the
// richer service metadata shape.
func CapabilityFromManifest(manifest *capability.Manifest, method, scope string) Capability {
	if manifest == nil {
		return Capability{Method: method, Scope: scope, Status: string(capability.NotValidated), Reason: "capability manifest is unavailable"}
	}
	entry, ok := manifest.Entry(method)
	if !ok {
		return Capability{Method: method, Scope: scope, Status: string(capability.NotValidated), Reason: "method is not registered"}
	}
	return Capability{Method: entry.Method, Scope: entry.Scope, Status: string(entry.Status), Reason: entry.Reason}
}
