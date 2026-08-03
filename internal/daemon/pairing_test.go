package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

func TestPairingConsumesUntilPrivateCodeBindsOneOwner(t *testing.T) {
	code := "A1B2C3D4"
	digest := sha256.Sum256([]byte(code))
	var bound, recipient string
	pairing, err := NewPairing(PairingConfig{
		BotUserID: "bot-user",
		CodeHash:  hex.EncodeToString(digest[:]),
		ExpiresAt: time.Now().Add(time.Minute),
		BindOwner: func(owner string) error { bound = owner; return nil },
		Send:      func(_ context.Context, _ string, target string) error { recipient = target; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := pairingEvent("owner-user", 1, "pair A1B2C3D4")
	wrong := pairingEvent("other-user", 1, "pair WRONG")
	if handled, err := pairing.Handle(context.Background(), wrong); err != nil || !handled || pairing.Accept(wrong) {
		t.Fatalf("wrong pairing = handled=%t accepted=%t err=%v", handled, pairing.Accept(wrong), err)
	}
	if handled, err := pairing.Handle(context.Background(), owner); err != nil || !handled || bound != "owner-user" || recipient != "owner-user" {
		t.Fatalf("owner pairing = handled=%t bound=%q recipient=%q err=%v", handled, bound, recipient, err)
	}
	if !pairing.Accept(owner) || pairing.Accept(wrong) {
		t.Fatalf("paired acceptance = owner:%t other:%t", pairing.Accept(owner), pairing.Accept(wrong))
	}
	if handled, err := pairing.Handle(context.Background(), owner); err != nil || handled {
		t.Fatalf("paired Handle() = %t, %v", handled, err)
	}
}

func TestPairingRejectsExpiredGroupAndBotMessages(t *testing.T) {
	code := "A1B2C3D4"
	digest := sha256.Sum256([]byte(code))
	paired := false
	pairing, err := NewPairing(PairingConfig{
		BotUserID: "bot-user",
		CodeHash:  hex.EncodeToString(digest[:]),
		ExpiresAt: time.Now().Add(-time.Second),
		BindOwner: func(string) error { paired = true; return nil },
		Send:      func(context.Context, string, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []contracts.SDKEvent{
		pairingEvent("owner-user", 1, "pair "+code),
		pairingEvent("owner-user", 2, "pair "+code),
		pairingEvent("bot-user", 1, "pair "+code),
	} {
		if handled, err := pairing.Handle(context.Background(), event); err != nil || !handled {
			t.Fatalf("Handle() = %t, %v", handled, err)
		}
	}
	if paired {
		t.Fatal("invalid pairing bound an owner")
	}
}

func TestInboundPairingNeverStartsProviderRun(t *testing.T) {
	harness := newHarness(t, false)
	defer harness.close(t)
	code := "A1B2C3D4"
	digest := sha256.Sum256([]byte(code))
	bound := ""
	pairing, err := NewPairing(PairingConfig{
		BotUserID: "bot-user",
		CodeHash:  hex.EncodeToString(digest[:]),
		ExpiresAt: time.Now().Add(time.Minute),
		BindOwner: func(owner string) error { bound = owner; return nil },
		Send:      func(context.Context, string, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	harness.inbound.pairing = pairing
	event := inboundEvent("pairing-event", "conversation-owner", "pairing-message")
	event.MessageText = "pair " + code
	outcome, err := harness.inbound.Process(context.Background(), event)
	if err != nil || !outcome.Created || !outcome.Ignored || outcome.RunID != "" || bound != "user-2" {
		t.Fatalf("pairing Process() = %#v, bound=%q, err=%v", outcome, bound, err)
	}
	if harness.policyCalls() != 0 || harness.provider.startCount() != 0 || harness.sender.calls() != 0 {
		t.Fatalf("pairing invoked policy=%d provider=%d reply=%d", harness.policyCalls(), harness.provider.startCount(), harness.sender.calls())
	}
}

func pairingEvent(sender string, sessionType int32, text string) contracts.SDKEvent {
	data, _ := json.Marshal(map[string]any{"sender_id": sender, "session_type": sessionType})
	return contracts.SDKEvent{Type: string(contracts.EventMessageReceived), Data: data, MessageText: text}
}
