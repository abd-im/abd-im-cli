package reply

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

func TestAgentRunUsesOneStructuredStreamAndTerminalPacket(t *testing.T) {
	store, service, _ := newTestService(t)
	defer store.Close()
	sender := &fakeStreamSender{}
	service.sender = sender
	if _, err := service.Reserve(context.Background(), Binding{
		ProfileID: "work", EventID: "event-1", ConversationID: "sg_group-1", TriggerMessageID: "trigger-1",
		GroupID: "group-1", RunID: "run-1",
	}); err != nil {
		t.Fatal(err)
	}
	run, err := service.NewAgentRun(context.Background(), "work", "event-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Queued(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := run.Started(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := run.Activity(context.Background(), contracts.TurnActivity{Kind: "activity.summary", Summary: "checked inputs"}); err != nil {
		t.Fatal(err)
	}
	if err := run.Answer(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if err := run.Answer(context.Background(), "hello world"); err != nil {
		t.Fatal(err)
	}
	if err := run.Complete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sender.starts != 1 || sender.delivery.Type != AgentRunStreamType {
		t.Fatalf("stream starts=%d type=%q", sender.starts, sender.delivery.Type)
	}
	var envelope AgentRunEnvelope
	if err := json.Unmarshal([]byte(sender.delivery.Content), &envelope); err != nil || envelope.Schema != 1 || envelope.RunID != "run-1" {
		t.Fatalf("envelope=%#v err=%v", envelope, err)
	}
	if len(sender.appends) != 5 || !sender.appends[len(sender.appends)-1].End {
		t.Fatalf("appends=%#v", sender.appends)
	}
	wantKinds := []string{"run.queued", "run.started", "activity.summary", "answer.delta", "run.completed"}
	for index, want := range wantKinds {
		if sender.appends[index].StartIndex != int64(index) || len(sender.appends[index].Packets) != 1 {
			t.Fatalf("append[%d]=%#v", index, sender.appends[index])
		}
		var packet AgentRunPacket
		if err := json.Unmarshal([]byte(sender.appends[index].Packets[0]), &packet); err != nil || packet.Kind != want {
			t.Fatalf("packet[%d]=%#v err=%v", index, packet, err)
		}
	}
}

func TestAgentRunMapsActivityKinds(t *testing.T) {
	activities := []contracts.TurnActivity{
		{Kind: "tool.started", CallID: "call-1", Name: "shell", Summary: "date"},
		{Kind: "tool.completed", CallID: "call-1", Status: "completed", DurationMS: 12, Summary: "done"},
		{Kind: "approval.requested", RequestID: "approval-1", Name: "shell", Summary: "run command", Choices: []string{"allow", "deny"}},
		{Kind: "approval.resolved", RequestID: "approval-1", Decision: "allow"},
		{Kind: "artifact", ArtifactName: "result.txt", MediaType: "text/plain", Size: 4, AttachmentID: "attachment-1"},
	}
	for _, activity := range activities {
		packet, ok := packetFromActivity(activity)
		if !ok || packet.Kind != activity.Kind || packet.CallID != activity.CallID || packet.RequestID != activity.RequestID {
			t.Fatalf("packetFromActivity(%#v) = %#v, %t", activity, packet, ok)
		}
	}
	if _, ok := packetFromActivity(contracts.TurnActivity{Kind: "provider.private"}); ok {
		t.Fatal("unknown activity kind was accepted")
	}
}

func TestAgentRunSplitsOversizedAnswerPacket(t *testing.T) {
	store, service, _ := newTestService(t)
	defer store.Close()
	sender := &fakeStreamSender{}
	service.sender = sender
	if _, err := service.Reserve(context.Background(), Binding{ProfileID: "work", EventID: "event-1", ConversationID: "sg_group-1", TriggerMessageID: "trigger-1", GroupID: "group-1", RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	run, _ := service.NewAgentRun(context.Background(), "work", "event-1")
	want := strings.Repeat("a", maxStreamPacketBytes)
	if err := run.Answer(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if err := run.Complete(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	for _, appendValue := range sender.appends {
		for _, raw := range appendValue.Packets {
			var packet AgentRunPacket
			if json.Unmarshal([]byte(raw), &packet) == nil && packet.Kind == "answer.delta" {
				got.WriteString(packet.Text)
			}
		}
	}
	if got.String() != want {
		t.Fatalf("answer length = %d, want %d", got.Len(), len(want))
	}
}

func TestAgentRunTruncatesOverLimitAnswerAndStillEnds(t *testing.T) {
	store, service, _ := newTestService(t)
	defer store.Close()
	sender := &fakeStreamSender{}
	service.sender = sender
	if _, err := service.Reserve(context.Background(), Binding{ProfileID: "work", EventID: "event-1", ConversationID: "sg_group-1", TriggerMessageID: "trigger-1", GroupID: "group-1", RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	run, _ := service.NewAgentRun(context.Background(), "work", "event-1")
	if err := run.Answer(context.Background(), strings.Repeat("界", maxAgentRunBytes)); err != nil {
		t.Fatal(err)
	}
	if err := run.Complete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.appends) == 0 || !sender.appends[len(sender.appends)-1].End {
		t.Fatalf("stream did not end: %#v", sender.appends)
	}
}

func TestAgentRunActivityBudgetPreservesAnswerCapacity(t *testing.T) {
	store, service, _ := newTestService(t)
	defer store.Close()
	sender := &fakeStreamSender{}
	service.sender = sender
	if _, err := service.Reserve(context.Background(), Binding{ProfileID: "work", EventID: "event-1", ConversationID: "sg_group-1", TriggerMessageID: "trigger-1", GroupID: "group-1", RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	run, _ := service.NewAgentRun(context.Background(), "work", "event-1")
	for index := 0; index < 100; index++ {
		if err := run.Activity(context.Background(), contracts.TurnActivity{Kind: "activity.summary", Summary: strings.Repeat("x", 1024)}); err != nil {
			t.Fatal(err)
		}
	}
	want := strings.Repeat("a", 64*1024)
	if err := run.Answer(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if err := run.Complete(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	for _, appendValue := range sender.appends {
		for _, raw := range appendValue.Packets {
			var packet AgentRunPacket
			if json.Unmarshal([]byte(raw), &packet) == nil && packet.Kind == "answer.delta" {
				got.WriteString(packet.Text)
			}
		}
	}
	if got.String() != want {
		t.Fatalf("answer length = %d, want %d", got.Len(), len(want))
	}
}
