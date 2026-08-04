// Package grant issues and verifies run-scoped provider permissions.
package grant

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidCredential = errors.New("invalid grant credential")
	ErrExpired           = errors.New("grant expired")
	ErrRevoked           = errors.New("grant revoked")
	ErrMethodDenied      = errors.New("method is not allowed by grant")
	ErrRateLimited       = errors.New("grant rate budget is exhausted")
)

// MessageWindow limits the history made available to a provider run.
type MessageWindow struct {
	ConversationID  string
	AfterMessageID  string
	BeforeMessageID string
}

// Policy is the complete authorization decision for one run.
type Policy struct {
	RunID               string
	ProfileID           string
	Principal           string
	Methods             []string
	MessageWindow       MessageWindow
	AttachmentByteLimit int64
	ExpiresAt           time.Time
	RateBudget          int
}

// Grant is the credential-free authorization state supplied to a typed handler.
type Grant struct {
	ID                  string
	RunID               string
	ProfileID           string
	Principal           string
	MessageWindow       MessageWindow
	AttachmentByteLimit int64
	ExpiresAt           time.Time
	RemainingBudget     int

	methods map[string]struct{}
}

// AllowsMethod reports whether the grant explicitly exposes a typed method.
func (g Grant) AllowsMethod(method string) bool {
	_, allowed := g.methods[method]
	return allowed
}

type storedGrant struct {
	grant   Grant
	revoked bool
}

// Store retains only hashed credentials. It is daemon-private and has no RPC
// or socket surface, so providers cannot use it to reach controller actions.
type Store struct {
	mu     sync.Mutex
	grants map[[32]byte]*storedGrant
}

func NewStore() *Store {
	return &Store{grants: make(map[[32]byte]*storedGrant)}
}

// Issue creates an opaque credential returned once to the daemon-owned proxy.
func (s *Store) Issue(policy Policy) (Grant, string, error) {
	if err := validatePolicy(policy); err != nil {
		return Grant{}, "", err
	}
	credential, err := randomCredential()
	if err != nil {
		return Grant{}, "", err
	}
	item := Grant{
		ID:                  newID(),
		RunID:               policy.RunID,
		ProfileID:           policy.ProfileID,
		Principal:           policy.Principal,
		MessageWindow:       policy.MessageWindow,
		AttachmentByteLimit: policy.AttachmentByteLimit,
		ExpiresAt:           policy.ExpiresAt,
		RemainingBudget:     policy.RateBudget,
		methods:             toSet(policy.Methods),
	}
	s.mu.Lock()
	s.grants[credentialHash(credential)] = &storedGrant{grant: item}
	s.mu.Unlock()
	return publicGrant(item), credential, nil
}

// Authorize validates and consumes one tool-call budget atomically.
func (s *Store) Authorize(credential, runID, profileID, method string) (Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, ok := s.grants[credentialHash(credential)]
	if !ok {
		return Grant{}, ErrInvalidCredential
	}
	item := stored.grant
	if stored.revoked {
		return Grant{}, ErrRevoked
	}
	if !time.Now().Before(item.ExpiresAt) {
		return Grant{}, ErrExpired
	}
	if item.RunID != runID || item.ProfileID != profileID {
		return Grant{}, ErrInvalidCredential
	}
	if _, allowed := item.methods[method]; !allowed {
		return Grant{}, ErrMethodDenied
	}
	if item.RemainingBudget <= 0 {
		return Grant{}, ErrRateLimited
	}
	stored.grant.RemainingBudget--
	return publicGrant(stored.grant), nil
}

// RevokeRun invalidates every active credential associated with a cancelled run.
func (s *Store) RevokeRun(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.grants {
		if item.grant.RunID == runID {
			item.revoked = true
		}
	}
}

func validatePolicy(policy Policy) error {
	if strings.TrimSpace(policy.RunID) == "" || strings.TrimSpace(policy.ProfileID) == "" || strings.TrimSpace(policy.Principal) == "" {
		return errors.New("grant run ID, profile ID, and principal are required")
	}
	if policy.ExpiresAt.IsZero() || !policy.ExpiresAt.After(time.Now()) {
		return errors.New("grant expiry must be in the future")
	}
	if policy.RateBudget <= 0 {
		return errors.New("grant rate budget must be positive")
	}
	if policy.AttachmentByteLimit < 0 {
		return errors.New("grant attachment byte limit must not be negative")
	}
	for _, method := range policy.Methods {
		if strings.TrimSpace(method) == "" {
			return errors.New("grant methods must not contain empty values")
		}
	}
	return nil
}

func publicGrant(item Grant) Grant {
	item.methods = cloneSet(item.methods)
	return item
}

func toSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func cloneSet(values map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for value := range values {
		result[value] = struct{}{}
	}
	return result
}

func randomCredential() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate grant credential: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}

func newID() string {
	credential, err := randomCredential()
	if err != nil {
		return fmt.Sprintf("grant-%d", time.Now().UnixNano())
	}
	return credential[:24]
}

func credentialHash(credential string) [32]byte {
	return sha256.Sum256([]byte(credential))
}
