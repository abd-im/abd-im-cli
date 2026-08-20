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

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

const SchemaVersion = "abdim.service/v1"

var (
	ErrInvalidArgument = errors.New("invalid service argument")
	ErrTargetDenied    = errors.New("service target is denied")
	ErrCursorInvalid   = errors.New("invalid cursor")
	ErrCursorExpired   = errors.New("cursor expired")
)

// Meta is attached to every successful typed response.
type Meta struct {
	ProfileID string `json:"profile_id"`
	Schema    string `json:"schema"`
	Stale     bool   `json:"stale"`
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

func NewMeta(profileID string, stale bool) Meta {
	return Meta{ProfileID: profileID, Schema: SchemaVersion, Stale: stale}
}

// ContractMeta converts a typed service result's metadata into the shared RPC
// envelope metadata without importing a service package into the transport.
func ContractMeta(meta Meta) contracts.Meta {
	return contracts.Meta{ProfileID: meta.ProfileID, Stale: meta.Stale, Schema: meta.Schema}
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
