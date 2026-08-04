package message

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/operation"
	"github.com/abd-im/abd-im-cli/internal/profile"
)

func TestMediaMethodsRequireMatchingAttachmentGrant(t *testing.T) {
	store, attachments := newMediaStore(t)
	defer store.Close()
	guard, err := operation.NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}

	sender := &fakeMediaSender{}
	handler, err := NewMedia(guard, attachments, sender)
	if err != nil {
		t.Fatal(err)
	}
	grants := grant.NewStore()
	access, credential, err := grants.Issue(grant.Policy{
		RunID: "run-1", ProfileID: "work", Principal: "provider",
		Methods: []string{ImageMethod, FileMethod, SoundMethod, VideoMethod},

		ExpiresAt: time.Now().Add(time.Hour), RateBudget: 16, AttachmentByteLimit: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	image := putMediaAttachment(t, attachments, access, AttachmentImage, "image")
	file := putMediaAttachment(t, attachments, access, AttachmentFile, "file")
	sound := putMediaAttachment(t, attachments, access, AttachmentSound, "sound")
	video := putMediaAttachment(t, attachments, access, AttachmentVideo, "video")
	thumbnail := putMediaAttachment(t, attachments, access, AttachmentImage, "thumbnail")
	tool, err := proxy.New(grants, "run-1", "work", handler.ProxyMethods())
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, key string, input MediaInput) contracts.Response {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		response, err := tool.Call(context.Background(), contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: key, ProfileID: "work", Method: method, Params: raw, Grant: credential, IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	if response := call(ImageMethod, "wrong-kind", MediaInput{AttachmentRef: file, FileName: "wrong.png", RecipientID: "user-1"}); response.Error == nil || response.Error.Code != contracts.CodePolicyDenied || sender.calls != 0 {
		t.Fatalf("wrong attachment kind = %+v, calls=%d", response, sender.calls)
	}
	for _, request := range []struct {
		method string
		input  MediaInput
	}{
		{ImageMethod, MediaInput{AttachmentRef: image, FileName: "photo.png", RecipientID: "user-1"}},
		{FileMethod, MediaInput{AttachmentRef: file, FileName: "report.txt", GroupID: "group-1"}},
		{SoundMethod, MediaInput{AttachmentRef: sound, FileName: "voice.ogg", DurationSeconds: 3, RecipientID: "user-1"}},
		{VideoMethod, MediaInput{AttachmentRef: video, FileName: "clip.mp4", DurationSeconds: 9, ThumbnailRef: thumbnail, ThumbnailFileName: "cover.png", RecipientID: "user-1"}},
	} {
		if response := call(request.method, "shared-key", request.input); !response.OK {
			t.Fatalf("allowed %s = %+v", request.method, response)
		}
	}
	if sender.calls != 4 || sender.payloads[0] != "image" || sender.payloads[1] != "file" || sender.payloads[2] != "sound" || sender.payloads[3] != "video:thumbnail" {
		t.Fatalf("media sender = %+v", sender)
	}
}

func TestMediaRejectsPathNamesAndUnknownOutcome(t *testing.T) {
	store, attachments := newMediaStore(t)
	defer store.Close()
	guard, err := operation.NewGuard(store)
	if err != nil {
		t.Fatal(err)
	}

	sender := &fakeMediaSender{err: operation.ErrOutcomeUnknown}
	handler, err := NewMedia(guard, attachments, sender)
	if err != nil {
		t.Fatal(err)
	}
	grants := grant.NewStore()
	access, credential, err := grants.Issue(grant.Policy{RunID: "run-1", ProfileID: "work", Principal: "provider", Methods: []string{ImageMethod}, ExpiresAt: time.Now().Add(time.Hour), RateBudget: 4, AttachmentByteLimit: 64})
	if err != nil {
		t.Fatal(err)
	}
	image := putMediaAttachment(t, attachments, access, AttachmentImage, "image")
	tool, err := proxy.New(grants, "run-1", "work", handler.ProxyMethods())
	if err != nil {
		t.Fatal(err)
	}
	call := func(key string, input MediaInput) contracts.Response {
		t.Helper()
		raw, _ := json.Marshal(input)
		response, _ := tool.Call(context.Background(), contracts.Request{APIVersion: contracts.APIVersionV1, RequestID: key, ProfileID: "work", Method: ImageMethod, Params: raw, Grant: credential, IdempotencyKey: key})
		return response
	}
	if response := call("path", MediaInput{AttachmentRef: image, FileName: "../../secret.png", RecipientID: "user-1"}); response.Error == nil || response.Error.Code != contracts.CodeInvalidArgument {
		t.Fatalf("path-like file name = %+v", response)
	}
	if response := call("duration", MediaInput{AttachmentRef: image, FileName: "photo.png", DurationSeconds: 1, RecipientID: "user-1"}); response.Error == nil || response.Error.Code != contracts.CodeInvalidArgument {
		t.Fatalf("unexpected image duration = %+v", response)
	}
	input := MediaInput{AttachmentRef: image, FileName: "photo.png", RecipientID: "user-1"}
	if response := call("unknown", input); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown {
		t.Fatalf("first unknown = %+v", response)
	}
	if response := call("new-key", input); response.Error == nil || response.Error.Code != contracts.CodeOutcomeUnknown || sender.calls != 1 {
		t.Fatalf("new-key unknown = %+v, calls=%d", response, sender.calls)
	}
}

func newMediaStore(t *testing.T) (*control.Store, *AttachmentStore) {
	t.Helper()
	root := t.TempDir()
	paths, err := profile.NewPaths(filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "runtime"), "work")
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsurePrivate(); err != nil {
		t.Fatal(err)
	}
	store, err := control.Open(paths.ControlDB)
	if err != nil {
		t.Fatal(err)
	}
	attachments, err := NewAttachmentStore(store, paths)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	return store, attachments
}

func putMediaAttachment(t *testing.T, store *AttachmentStore, item grant.Grant, kind AttachmentKind, value string) string {
	t.Helper()
	attachment, err := store.Put(context.Background(), item, kind, strings.NewReader(value))
	if err != nil {
		t.Fatal(err)
	}
	return attachment.ID
}

type fakeMediaSender struct {
	calls    int
	payloads []string
	err      error
}

func (s *fakeMediaSender) SendImage(_ context.Context, payload MediaPayload, _, _ string) error {
	s.record(payload)
	return s.err
}

func (s *fakeMediaSender) SendFile(_ context.Context, payload MediaPayload, _, _ string) error {
	s.record(payload)
	return s.err
}

func (s *fakeMediaSender) SendSound(_ context.Context, payload MediaPayload, _ int64, _, _ string) error {
	s.record(payload)
	return s.err
}

func (s *fakeMediaSender) SendVideo(_ context.Context, payload, thumbnail MediaPayload, _ int64, _, _ string) error {
	value, _ := io.ReadAll(payload.Reader)
	thumb, _ := io.ReadAll(thumbnail.Reader)
	s.calls++
	s.payloads = append(s.payloads, string(value)+":"+string(thumb))
	return s.err
}

func (s *fakeMediaSender) record(payload MediaPayload) {
	value, _ := io.ReadAll(payload.Reader)
	s.calls++
	s.payloads = append(s.payloads, string(value))
}

var _ MediaSender = (*fakeMediaSender)(nil)
