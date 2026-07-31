package capability

import "testing"

func TestEntriesAreSortedAndRetainVerificationState(t *testing.T) {
	manifest, err := New([]Entry{
		{Method: "message.history", Scope: "message.read", Status: NotValidated, Reason: "integration pending"},
		{Method: "conversation.list", Scope: "conversation.read", Status: Available},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	entries := manifest.Entries()
	if len(entries) != 2 || entries[0].Method != "conversation.list" || entries[1].Status != NotValidated {
		t.Fatalf("Entries() = %+v", entries)
	}
	if manifest.Allows("message.history", "message.read") {
		t.Fatal("Allows() accepted an unverified capability")
	}
}
