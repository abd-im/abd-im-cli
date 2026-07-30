package grant

import (
	"errors"
	"testing"
	"time"
)

func TestAuthorizeRequiresRunMethodScopeTargetAndBudget(t *testing.T) {
	store := NewStore()
	_, credential, err := store.Issue(Policy{
		RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{"message.history"}, Scopes: []string{"message.read"}, TargetAllowlist: []string{"conversation-1"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 1,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := store.Authorize(credential, "run-1", "work", "message.history", "message.read", []string{"conversation-2"}); !errors.Is(err, ErrTargetDenied) {
		t.Fatalf("unauthorized target error = %v, want ErrTargetDenied", err)
	}
	access, err := store.Authorize(credential, "run-1", "work", "message.history", "message.read", []string{"conversation-1"})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if access.RemainingBudget != 0 || !access.AllowsTarget("conversation-1") {
		t.Fatalf("access = %#v", access)
	}
	if _, err := store.Authorize(credential, "run-1", "work", "message.history", "message.read", []string{"conversation-1"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("budget error = %v, want ErrRateLimited", err)
	}
}

func TestGrantExpiryAndRevocationFailClosed(t *testing.T) {
	store := NewStore()
	_, expired, err := store.Issue(Policy{RunID: "run-expired", ProfileID: "work", Principal: "provider", Methods: []string{"profile.get"}, Scopes: []string{"profile.read"}, ExpiresAt: time.Now().Add(time.Millisecond), RateBudget: 1})
	if err != nil {
		t.Fatalf("Issue(expired) error = %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := store.Authorize(expired, "run-expired", "work", "profile.get", "profile.read", nil); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired Authorize() error = %v, want ErrExpired", err)
	}
	_, credential, err := store.Issue(Policy{RunID: "run-revoked", ProfileID: "work", Principal: "provider", Methods: []string{"profile.get"}, Scopes: []string{"profile.read"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 1})
	if err != nil {
		t.Fatalf("Issue(revoked) error = %v", err)
	}
	store.RevokeRun("run-revoked")
	if _, err := store.Authorize(credential, "run-revoked", "work", "profile.get", "profile.read", nil); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked Authorize() error = %v, want ErrRevoked", err)
	}
}
