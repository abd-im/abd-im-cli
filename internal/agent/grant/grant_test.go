package grant

import (
	"errors"
	"testing"
	"time"
)

func TestAuthorizeRequiresRunProfileMethodAndBudget(t *testing.T) {
	store := NewStore()
	_, credential, err := store.Issue(Policy{
		RunID: "run-1", ProfileID: "work", Principal: "provider",
		Methods: []string{"message.history"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(credential, "other-run", "work", "message.history"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("wrong run error = %v", err)
	}
	if _, err := store.Authorize(credential, "run-1", "work", "message.send_text"); !errors.Is(err, ErrMethodDenied) {
		t.Fatalf("wrong method error = %v", err)
	}
	access, err := store.Authorize(credential, "run-1", "work", "message.history")
	if err != nil || access.RemainingBudget != 0 || !access.AllowsMethod("message.history") {
		t.Fatalf("Authorize() = %+v, %v", access, err)
	}
	if _, err := store.Authorize(credential, "run-1", "work", "message.history"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("budget error = %v", err)
	}
}

func TestGrantExpiryAndRevocationFailClosed(t *testing.T) {
	store := NewStore()
	_, expired, err := store.Issue(Policy{RunID: "run-expired", ProfileID: "work", Principal: "provider", Methods: []string{"profile.get"}, ExpiresAt: time.Now().Add(time.Millisecond), RateBudget: 1})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := store.Authorize(expired, "run-expired", "work", "profile.get"); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired error = %v", err)
	}
	_, credential, err := store.Issue(Policy{RunID: "run-revoked", ProfileID: "work", Principal: "provider", Methods: []string{"profile.get"}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 1})
	if err != nil {
		t.Fatal(err)
	}
	store.RevokeRun("run-revoked")
	if _, err := store.Authorize(credential, "run-revoked", "work", "profile.get"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked error = %v", err)
	}
}

func TestGrantCarriesConversationAndAttachmentLimits(t *testing.T) {
	store := NewStore()
	window := MessageWindow{ConversationID: "conversation-1", AfterMessageID: "after", BeforeMessageID: "before"}
	issued, _, err := store.Issue(Policy{
		RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{"message.history"},
		MessageWindow: window, AttachmentByteLimit: 1024, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.MessageWindow != window || issued.AttachmentByteLimit != 1024 {
		t.Fatalf("grant limits = %+v", issued)
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
	if _, err := store.Authorize(credential, "run-reply", "work", "message.send_text"); !errors.Is(err, ErrMethodDenied) {
		t.Fatalf("reply-only authorization = %v", err)
	}
}
