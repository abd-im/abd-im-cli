package capability

import "testing"

var testedCompatibility = Compatibility{
	Provider:    "codex",
	MCPProtocol: "2026-07-28",
	SDKVersion:  "3.8.0",
	ServerAPI:   "openim-api/v3",
}

func TestEvidenceGateKeepsOnlySupportedStaticAvailableEntries(t *testing.T) {
	gate, err := NewEvidenceGate([]Compatibility{testedCompatibility})
	if err != nil {
		t.Fatal(err)
	}
	entries := []Entry{
		{Method: "message.history", Scope: "message.read", Status: Available},
		{Method: "conversation.unread", Scope: "conversation.read", Status: NotValidated, Reason: "no server source"},
	}

	manifest, err := gate.Manifest(testedCompatibility, entries)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Allows("message.history", "message.read") {
		t.Fatal("supported compatibility did not retain available capability")
	}
	entry, ok := manifest.Entry("conversation.unread")
	if !ok || entry.Status != NotValidated || entry.Reason != "no server source" {
		t.Fatalf("unvalidated entry = %#v, exists=%v", entry, ok)
	}
}

func TestEvidenceGateFailsClosedForUnverifiedCombination(t *testing.T) {
	gate, err := NewEvidenceGate([]Compatibility{testedCompatibility})
	if err != nil {
		t.Fatal(err)
	}
	unsupported := testedCompatibility
	unsupported.SDKVersion = "3.8.1"
	manifest, err := gate.Manifest(unsupported, []Entry{{Method: "message.history", Scope: "message.read", Status: Available}})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Allows("message.history", "message.read") {
		t.Fatal("static available entry bypassed missing compatibility evidence")
	}
	entry, ok := manifest.Entry("message.history")
	if !ok || entry.Status != NotValidated || entry.Reason != compatibilityNotValidatedReason {
		t.Fatalf("downgraded entry = %#v, exists=%v", entry, ok)
	}
}

func TestEvidenceGateRequiresUniqueCompleteEvidence(t *testing.T) {
	if _, err := NewEvidenceGate(nil); err == nil {
		t.Fatal("NewEvidenceGate(nil) succeeded")
	}
	if _, err := NewEvidenceGate([]Compatibility{{Provider: "codex"}}); err == nil {
		t.Fatal("NewEvidenceGate accepted incomplete evidence")
	}
	if _, err := NewEvidenceGate([]Compatibility{testedCompatibility, testedCompatibility}); err == nil {
		t.Fatal("NewEvidenceGate accepted duplicate evidence")
	}
}
