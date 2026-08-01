package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/logsafe"
)

const (
	privacyTokenMarker   = "e2e-token-marker-7f4c"
	privacyMessageMarker = "e2e-message-marker-8a31"
)

func TestTokenAndInboundBodyStayOutOfVisibleBoundariesE2E(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "control.db")
	harness := newRuntimeHarness(t, databasePath, filepath.Join(root, "runtime", "daemon.lock"), filepath.Join(root, "runtime", "daemon.sock"))
	defer harness.close(t)
	harness.start(t)

	event := e2eInboundEvent("privacy-event")
	event.MessageText = privacyMessageMarker + " " + privacyTokenMarker
	if err := harness.sdk.Emit(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if delivery := receiveDelivery(t, harness.sender.deliveries); strings.Contains(delivery.Text, privacyTokenMarker) || strings.Contains(delivery.Text, privacyMessageMarker) {
		t.Fatalf("reply leaked inbound marker: %#v", delivery)
	}

	response, err := ipc.Call(context.Background(), harness.socketPath, contracts.Request{
		APIVersion: contracts.APIVersionV1,
		RequestID:  "privacy-owner-rpc",
		ProfileID:  "work",
		Method:     "profile.get",
		Params:     json.RawMessage(`{}`),
	})
	if err != nil || !response.OK {
		t.Fatalf("owner RPC = %#v, %v", response, err)
	}
	assertNoPrivacyMarker(t, "owner RPC", marshalPrivacyValue(t, response))
	batch, err := harness.ledger.List(context.Background(), "work", "", 10)
	if err != nil || len(batch.Events) != 1 {
		t.Fatalf("ledger.List() = %#v, %v", batch, err)
	}
	assertNoPrivacyMarker(t, "event ledger", marshalPrivacyValue(t, batch))

	harness.stop(t)
	if err := harness.store.Close(); err != nil {
		t.Fatal(err)
	}
	harness.store = nil
	contents, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	assertNoPrivacyMarker(t, "control database", contents)

	var logs bytes.Buffer
	logger := logsafe.NewLogger(&logs, privacyTokenMarker)
	logger.SDK("token=%s request=%s", privacyTokenMarker, privacyMessageMarker)
	logger.HTTP("authorization: Bearer %s request=%s", privacyTokenMarker, privacyMessageMarker)
	logger.WebSocket("wss://example.test/connect?token=%s request=%s", privacyTokenMarker, privacyMessageMarker)
	logger.Stderr("token=%s request=%s", privacyTokenMarker, privacyMessageMarker)
	logger.Audit("token=%s request=%s", privacyTokenMarker, privacyMessageMarker)
	assertNoPrivacyMarker(t, "diagnostic logs", logs.Bytes())
}

func marshalPrivacyValue(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func assertNoPrivacyMarker(t *testing.T, boundary string, value []byte) {
	t.Helper()
	for _, marker := range []string{privacyTokenMarker, privacyMessageMarker} {
		if bytes.Contains(value, []byte(marker)) {
			t.Fatalf("%s leaked marker %q: %q", boundary, marker, value)
		}
	}
}
