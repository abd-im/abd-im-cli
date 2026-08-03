package grant

import (
	"errors"
	"testing"
	"time"
)

func TestAuthorizeRequiresRunMethodScopeTargetAndBudget(t *testing.T) {
	store := NewStore()
	_, credential, err := store.Issue(Policy{
		RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{"message.history"}, Scopes: []string{"message.read"}, TargetAllowlists: map[string][]string{"message.history": {ConversationTarget("conversation-1")}}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 1,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := store.Authorize(credential, "run-1", "work", "message.history", "message.read", []string{ConversationTarget("conversation-2")}); !errors.Is(err, ErrTargetDenied) {
		t.Fatalf("unauthorized target error = %v, want ErrTargetDenied", err)
	}
	access, err := store.Authorize(credential, "run-1", "work", "message.history", "message.read", []string{ConversationTarget("conversation-1")})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if access.RemainingBudget != 0 || !access.AllowsMethod("message.history") || !access.AllowsTarget("message.history", ConversationTarget("conversation-1")) {
		t.Fatalf("access = %#v", access)
	}
	if _, err := store.Authorize(credential, "run-1", "work", "message.history", "message.read", []string{ConversationTarget("conversation-1")}); !errors.Is(err, ErrRateLimited) {
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

func TestTargetsAreScopedToMethodAndResource(t *testing.T) {
	store := NewStore()
	_, credential, err := store.Issue(Policy{
		RunID:     "run-1",
		ProfileID: "work",
		Principal: "provider",
		Methods:   []string{"message.history", "message.send_text"},
		Scopes:    []string{"message.read", "message.send"},
		TargetAllowlists: map[string][]string{
			"message.history":   {ConversationTarget("shared-id")},
			"message.send_text": {UserTarget("shared-id")},
		},
		ExpiresAt:  time.Now().Add(time.Hour),
		RateBudget: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(credential, "run-1", "work", "message.history", "message.read", []string{UserTarget("shared-id")}); !errors.Is(err, ErrTargetDenied) {
		t.Fatalf("user target accepted for message.history: %v", err)
	}
	if _, err := store.Authorize(credential, "run-1", "work", "message.send_text", "message.send", []string{ConversationTarget("shared-id")}); !errors.Is(err, ErrTargetDenied) {
		t.Fatalf("conversation target accepted for message.send_text: %v", err)
	}
	if _, err := store.Authorize(credential, "run-1", "work", "message.history", "message.read", []string{ConversationTarget("shared-id")}); err != nil {
		t.Fatalf("message.history authorized target: %v", err)
	}
	if _, err := store.Authorize(credential, "run-1", "work", "message.send_text", "message.send", []string{UserTarget("shared-id")}); err != nil {
		t.Fatalf("message.send_text authorized target: %v", err)
	}
	if _, _, err := store.Issue(Policy{
		RunID: "run-2", ProfileID: "work", Principal: "provider",
		Methods: []string{"message.history"}, Scopes: []string{"message.read"},
		TargetAllowlists: map[string][]string{"message.send_text": {UserTarget("user-1")}},
		ExpiresAt:        time.Now().Add(time.Hour), RateBudget: 1,
	}); err == nil {
		t.Fatal("Issue() accepted targets for an ungranted method")
	}
}

func TestAnyTargetRemainsScopedToItsMethod(t *testing.T) {
	store := NewStore()
	_, credential, err := store.Issue(Policy{
		RunID: "run-any", ProfileID: "work", Principal: "provider",
		Methods: []string{"message.history", "message.send_text"}, Scopes: []string{"message.read", "message.send"},
		TargetAllowlists: map[string][]string{"message.history": {AnyTarget}},
		ExpiresAt:        time.Now().Add(time.Hour), RateBudget: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(credential, "run-any", "work", "message.history", "message.read", []string{ConversationTarget("conversation-2")}); err != nil {
		t.Fatalf("wildcard target was not authorized: %v", err)
	}
	if _, err := store.Authorize(credential, "run-any", "work", "message.send_text", "message.send", []string{UserTarget("user-2")}); !errors.Is(err, ErrTargetDenied) {
		t.Fatalf("wildcard escaped its method: %v", err)
	}
}

func TestMessageTargetsCannotBeUsedAsConversationTargets(t *testing.T) {
	store := NewStore()
	_, credential, err := store.Issue(Policy{
		RunID:     "run-1",
		ProfileID: "work",
		Principal: "provider",
		Methods:   []string{"message.send_quote"},
		Scopes:    []string{"message.send_quote"},
		TargetAllowlists: map[string][]string{
			"message.send_quote": {ConversationTarget("shared-id"), MessageTarget("shared-id")},
		},
		ExpiresAt:  time.Now().Add(time.Hour),
		RateBudget: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(credential, "run-1", "work", "message.send_quote", "message.send_quote", []string{UserTarget("shared-id")}); !errors.Is(err, ErrTargetDenied) {
		t.Fatalf("user target accepted for quote: %v", err)
	}
	if _, err := store.Authorize(credential, "run-1", "work", "message.send_quote", "message.send_quote", []string{ConversationTarget("shared-id"), MessageTarget("shared-id")}); err != nil {
		t.Fatalf("quote targets not authorized: %v", err)
	}
}

func TestGrantCarriesAttachmentByteLimit(t *testing.T) {
	store := NewStore()
	issued, _, err := store.Issue(Policy{
		RunID: "run-attachment", ProfileID: "work", Principal: "provider",
		Methods: []string{"message.send_image"}, Scopes: []string{"message.send_image"},
		ExpiresAt: time.Now().Add(time.Hour), RateBudget: 1, AttachmentByteLimit: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.AttachmentByteLimit != 1024 {
		t.Fatalf("AttachmentByteLimit = %d, want 1024", issued.AttachmentByteLimit)
	}
	if _, _, err := store.Issue(Policy{
		RunID: "run-invalid", ProfileID: "work", Principal: "provider",
		Methods: []string{"message.send_image"}, Scopes: []string{"message.send_image"},
		ExpiresAt: time.Now().Add(time.Hour), RateBudget: 1, AttachmentByteLimit: -1,
	}); err == nil {
		t.Fatal("Issue() accepted a negative attachment byte limit")
	}
}

func TestReplyOnlyGrantExposesNoTypedMethods(t *testing.T) {
	store := NewStore()
	issued, credential, err := store.Issue(Policy{
		RunID: "run-reply", ProfileID: "work", Principal: "openim:user-1",
		ExpiresAt: time.Now().Add(time.Hour), RateBudget: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Principal != "openim:user-1" {
		t.Fatalf("reply-only grant = %+v", issued)
	}
	if _, err := store.Authorize(credential, "run-reply", "work", "message.send_text", "message.send", []string{UserTarget("user-1")}); !errors.Is(err, ErrMethodDenied) {
		t.Fatalf("reply-only grant authorization = %v, want ErrMethodDenied", err)
	}
	if _, _, err := store.Issue(Policy{
		RunID: "run-invalid", ProfileID: "work", Principal: "provider",
		Methods: []string{"message.send_text"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 1,
	}); err == nil {
		t.Fatal("Issue() accepted methods without scopes")
	}
}
