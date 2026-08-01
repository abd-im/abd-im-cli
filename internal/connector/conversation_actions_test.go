package connector

import "testing"

func TestPreparedConversationSettingsRequiresAdapter(t *testing.T) {
	if _, err := (*Prepared)(nil).ConversationSettings(); err == nil {
		t.Fatal("nil Prepared ConversationSettings() error = nil")
	}
	if _, err := (&Prepared{}).ConversationSettings(); err == nil {
		t.Fatal("empty Prepared ConversationSettings() error = nil")
	}
}
