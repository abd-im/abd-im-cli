package reply

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestStreamCreatesOnceAppendsSuffixesAndEnds(t *testing.T) {
	store, service, _ := newTestService(t)
	defer store.Close()
	sender := &fakeStreamSender{}
	service.sender = sender
	slot, err := service.Reserve(context.Background(), Binding{
		ProfileID: "work", EventID: "event-1", ConversationID: "si_bot_user", TriggerMessageID: "trigger-1",
		RecipientID: "user", RunID: "run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := service.NewStream(context.Background(), "work", "event-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"hel", "hello", "hello world"} {
		if err := stream.Update(context.Background(), text); err != nil {
			t.Fatal(err)
		}
	}
	if err := stream.Finish(context.Background(), "hello world"); err != nil {
		t.Fatal(err)
	}
	if sender.starts != 1 || sender.delivery.ClientMsgID != slot.OperationID || sender.delivery.Content != "hel" {
		t.Fatalf("start = %d, delivery = %#v", sender.starts, sender.delivery)
	}
	if len(sender.appends) != 3 || sender.appends[0].StartIndex != 0 || sender.appends[0].Packets[0] != "lo" ||
		sender.appends[1].StartIndex != 1 || sender.appends[1].Packets[0] != " world" ||
		sender.appends[2].StartIndex != 2 || !sender.appends[2].End {
		t.Fatalf("appends = %#v", sender.appends)
	}
}

func TestStreamRejectsReplacementAndClosesExistingMessage(t *testing.T) {
	store, service, _ := newTestService(t)
	defer store.Close()
	sender := &fakeStreamSender{}
	service.sender = sender
	if _, err := service.Reserve(context.Background(), Binding{ProfileID: "work", EventID: "event-1", ConversationID: "si_bot_user", TriggerMessageID: "trigger-1", RecipientID: "user", RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	stream, _ := service.NewStream(context.Background(), "work", "event-1")
	if err := stream.Update(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if err := stream.Finish(context.Background(), "rewritten"); !errors.Is(err, ErrNonMonotonicStream) {
		t.Fatalf("Finish() error = %v", err)
	}
	if sender.starts != 1 || len(sender.appends) != 1 || !sender.appends[0].End {
		t.Fatalf("starts = %d, appends = %#v", sender.starts, sender.appends)
	}
}

func TestSplitStreamTextKeepsUTF8PacketsWithinLimit(t *testing.T) {
	value := strings.Repeat("a", maxStreamPacketBytes-1) + "界" + "tail"
	packets := splitStreamText(value)
	if strings.Join(packets, "") != value || len(packets) != 2 {
		t.Fatalf("split packets = %d", len(packets))
	}
	for _, packet := range packets {
		if len(packet) > maxStreamPacketBytes {
			t.Fatalf("packet length = %d", len(packet))
		}
	}
}

type fakeStreamSender struct {
	starts   int
	delivery StreamDelivery
	appends  []StreamAppend
}

func (s *fakeStreamSender) Reply(context.Context, Delivery) error { return nil }

func (s *fakeStreamSender) StartStream(_ context.Context, delivery StreamDelivery) (StreamRef, error) {
	s.starts++
	s.delivery = delivery
	return StreamRef{ConversationID: delivery.ConversationID, ClientMsgID: delivery.ClientMsgID}, nil
}

func (s *fakeStreamSender) AppendStream(_ context.Context, appendValue StreamAppend) error {
	appendValue.Packets = append([]string(nil), appendValue.Packets...)
	s.appends = append(s.appends, appendValue)
	return nil
}
