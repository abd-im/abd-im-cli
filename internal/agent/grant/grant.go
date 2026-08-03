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
	ErrScopeDenied       = errors.New("scope is not allowed by grant")
	ErrTargetDenied      = errors.New("target is not allowed by grant")
	ErrRateLimited       = errors.New("grant rate budget is exhausted")
)

const (
	TargetConversation = "conversation"
	TargetGroup        = "group"
	TargetMessage      = "message"
	TargetUser         = "user"
)

// Target gives an allowlist entry a stable resource namespace. It prevents a
// user, group, and conversation that share an ID from being interchangeable.
func Target(resource, id string) string {
	if strings.TrimSpace(resource) == "" || strings.TrimSpace(id) == "" {
		return ""
	}
	return resource + ":" + id
}

func ConversationTarget(id string) string { return Target(TargetConversation, id) }
func GroupTarget(id string) string        { return Target(TargetGroup, id) }
func MessageTarget(id string) string      { return Target(TargetMessage, id) }
func UserTarget(id string) string         { return Target(TargetUser, id) }

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
	Scopes              []string
	TargetAllowlists    map[string][]string
	MessageWindow       MessageWindow
	FullAccess          bool
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
	fullAccess          bool

	methods map[string]struct{}
	scopes  map[string]struct{}
	targets map[string]map[string]struct{}
}

// AllowsScope reports whether a handler may use its required capability scope.
func (g Grant) AllowsScope(scope string) bool {
	_, allowed := g.scopes[scope]
	return allowed
}

// AllowsMethod reports whether the grant explicitly exposes a typed method.
// It is kept separate from scope checks because two methods may share a
// scope while still having different target and parameter contracts.
func (g Grant) AllowsMethod(method string) bool {
	_, allowed := g.methods[method]
	return allowed
}

// AllowsTarget reports whether a typed target belongs to this method's
// allowlist. An empty target is always safe for target-free typed methods.
func (g Grant) AllowsTarget(method, target string) bool {
	if target == "" {
		return true
	}
	if g.fullAccess {
		return true
	}
	_, allowed := g.targets[method][target]
	return allowed
}

// FullAccess is an explicit policy property for a trusted owner run. It is
// never inferred from a method, scope, or target value.
func (g Grant) FullAccess() bool { return g.fullAccess }

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
		fullAccess:          policy.FullAccess,
		methods:             toSet(policy.Methods),
		scopes:              toSet(policy.Scopes),
		targets:             toMethodTargetSets(policy.TargetAllowlists),
	}
	s.mu.Lock()
	s.grants[credentialHash(credential)] = &storedGrant{grant: item}
	s.mu.Unlock()
	return publicGrant(item), credential, nil
}

// Authorize validates and consumes one tool-call budget atomically.
func (s *Store) Authorize(credential, runID, profileID, method, scope string, targets []string) (Grant, error) {
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
	if _, allowed := item.scopes[scope]; !allowed {
		return Grant{}, ErrScopeDenied
	}
	for _, target := range targets {
		if !item.AllowsTarget(method, target) {
			return Grant{}, ErrTargetDenied
		}
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
	if len(policy.Methods) == 0 || len(policy.Scopes) == 0 {
		return errors.New("grant methods and scopes are required")
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
	methods := toSet(policy.Methods)
	for _, values := range [][]string{policy.Methods, policy.Scopes} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return errors.New("grant allowlists must not contain empty values")
			}
		}
	}
	for method, targets := range policy.TargetAllowlists {
		if strings.TrimSpace(method) == "" {
			return errors.New("grant target method must not be empty")
		}
		if _, allowed := methods[method]; !allowed {
			return errors.New("grant target method must be allowed")
		}
		for _, target := range targets {
			if !validTarget(target) {
				return errors.New("grant targets must not be empty")
			}
		}
	}
	return nil
}

func validTarget(target string) bool {
	resource, id, found := strings.Cut(target, ":")
	return found && strings.TrimSpace(resource) != "" && strings.TrimSpace(id) != ""
}

func publicGrant(item Grant) Grant {
	item.methods = cloneSet(item.methods)
	item.scopes = cloneSet(item.scopes)
	item.targets = cloneMethodTargetSets(item.targets)
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

func toMethodTargetSets(values map[string][]string) map[string]map[string]struct{} {
	result := make(map[string]map[string]struct{}, len(values))
	for method, targets := range values {
		result[method] = toSet(targets)
	}
	return result
}

func cloneMethodTargetSets(values map[string]map[string]struct{}) map[string]map[string]struct{} {
	result := make(map[string]map[string]struct{}, len(values))
	for method, targets := range values {
		result[method] = cloneSet(targets)
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
