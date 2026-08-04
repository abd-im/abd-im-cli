package message

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/operation"
)

const (
	ImageMethod = "message.send_image"
	ImageScope  = "message.send_image"
	FileMethod  = "message.send_file"
	FileScope   = "message.send_file"
	SoundMethod = "message.send_sound"
	SoundScope  = "message.send_sound"
	VideoMethod = "message.send_video"
	VideoScope  = "message.send_video"

	maxMediaFileNameBytes = 255
	maxMediaDuration      = 4 * 60 * 60
)

// MediaInput identifies one daemon-held attachment. It deliberately has no
// local path or content field.
type MediaInput struct {
	AttachmentRef     string `json:"attachment_ref"`
	FileName          string `json:"file_name"`
	DurationSeconds   int64  `json:"duration_seconds,omitempty"`
	ThumbnailRef      string `json:"thumbnail_ref,omitempty"`
	ThumbnailFileName string `json:"thumbnail_file_name,omitempty"`
	RecipientID       string `json:"recipient_id,omitempty"`
	GroupID           string `json:"group_id,omitempty"`
}

// MediaPayload is an in-process, daemon-only media stream. It is never
// serialized to a provider, control record, audit record, or log sink.
type MediaPayload struct {
	Reader   io.Reader
	FileName string
}

// MediaSender is the narrow daemon-owned SDK upload boundary.
type MediaSender interface {
	SendImage(context.Context, MediaPayload, string, string) error
	SendFile(context.Context, MediaPayload, string, string) error
	SendSound(context.Context, MediaPayload, int64, string, string) error
	SendVideo(context.Context, MediaPayload, MediaPayload, int64, string, string) error
}

type mediaSpec struct {
	method            string
	scope             string
	kind              AttachmentKind
	requiresDuration  bool
	requiresThumbnail bool
}

var mediaSpecs = []mediaSpec{
	{method: ImageMethod, scope: ImageScope, kind: AttachmentImage},
	{method: FileMethod, scope: FileScope, kind: AttachmentFile},
	{method: SoundMethod, scope: SoundScope, kind: AttachmentSound, requiresDuration: true},
	{method: VideoMethod, scope: VideoScope, kind: AttachmentVideo, requiresDuration: true, requiresThumbnail: true},
}

// MediaHandler exposes the fixed image, file, sound, and video action family.
type MediaHandler struct {
	guard       *operation.Guard
	attachments *AttachmentStore
	sender      MediaSender
}

func NewMedia(guard *operation.Guard, attachments *AttachmentStore, sender MediaSender) (*MediaHandler, error) {
	if guard == nil || attachments == nil || sender == nil {
		return nil, errors.New("operation guard, attachment store, and media sender are required")
	}
	return &MediaHandler{guard: guard, attachments: attachments, sender: sender}, nil
}

// ProxyMethods returns the complete media capability family.
func (h *MediaHandler) ProxyMethods() []proxy.Method {
	methods := make([]proxy.Method, 0, len(mediaSpecs))
	for _, value := range mediaSpecs {
		spec := value
		methods = append(methods, proxy.Method{
			Name: spec.method,
			Handle: func(ctx context.Context, request contracts.Request, item grant.Grant) (json.RawMessage, error) {
				return h.handle(ctx, request, item, spec)
			},
		})
	}
	return methods
}

func (h *MediaHandler) handle(ctx context.Context, request contracts.Request, item grant.Grant, spec mediaSpec) (json.RawMessage, error) {
	input, err := parseMedia(request.Params, spec)
	if err != nil {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, "invalid "+spec.method+" input")
	}
	if request.IdempotencyKey == "" {
		return nil, proxy.Failure(contracts.CodeInvalidArgument, spec.method+" requires idempotency_key")
	}
	outcome, err := h.guard.Execute(ctx, operation.Request{
		ID:             strings.ReplaceAll(spec.method, ".", "-") + "-" + request.IdempotencyKey,
		ProfileID:      request.ProfileID,
		Scope:          spec.scope,
		IdempotencyKey: request.IdempotencyKey,
		Input:          input,
	}, func(ctx context.Context) error {
		attachment, _, err := h.attachments.Open(ctx, item, input.AttachmentRef, spec.kind)
		if err != nil {
			return err
		}
		defer attachment.Close()

		payload := MediaPayload{Reader: attachment, FileName: input.FileName}
		switch spec.kind {
		case AttachmentImage:
			return h.sender.SendImage(ctx, payload, input.RecipientID, input.GroupID)
		case AttachmentFile:
			return h.sender.SendFile(ctx, payload, input.RecipientID, input.GroupID)
		case AttachmentSound:
			return h.sender.SendSound(ctx, payload, input.DurationSeconds, input.RecipientID, input.GroupID)
		case AttachmentVideo:
			thumbnail, _, err := h.attachments.Open(ctx, item, input.ThumbnailRef, AttachmentImage)
			if err != nil {
				return err
			}
			defer thumbnail.Close()
			return h.sender.SendVideo(ctx, payload, MediaPayload{Reader: thumbnail, FileName: input.ThumbnailFileName}, input.DurationSeconds, input.RecipientID, input.GroupID)
		default:
			return errors.New("unsupported media attachment kind")
		}
	})
	if err != nil {
		if errors.Is(err, ErrAttachmentAccess) || errors.Is(err, ErrAttachmentExpired) {
			return nil, proxy.Failure(contracts.CodePolicyDenied, "attachment is not authorized for this run")
		}
		return nil, messageActionFailure(err, spec.method)
	}
	return messageActionResult(outcome.Operation.ID, string(outcome.Operation.Status))
}

func parseMedia(raw json.RawMessage, spec mediaSpec) (MediaInput, error) {
	var input MediaInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return MediaInput{}, err
	}
	input.AttachmentRef = strings.TrimSpace(input.AttachmentRef)
	input.FileName = strings.TrimSpace(input.FileName)
	input.ThumbnailRef = strings.TrimSpace(input.ThumbnailRef)
	input.ThumbnailFileName = strings.TrimSpace(input.ThumbnailFileName)
	input.RecipientID = strings.TrimSpace(input.RecipientID)
	input.GroupID = strings.TrimSpace(input.GroupID)
	if input.AttachmentRef == "" || !validMediaFileName(input.FileName) {
		return MediaInput{}, errors.New("attachment reference and file name are required")
	}
	if (input.RecipientID == "" && input.GroupID == "") || (input.RecipientID != "" && input.GroupID != "") {
		return MediaInput{}, errors.New("exactly one message recipient is required")
	}
	if spec.requiresDuration && (input.DurationSeconds <= 0 || input.DurationSeconds > maxMediaDuration) {
		return MediaInput{}, errors.New("media duration is invalid")
	}
	if !spec.requiresDuration && input.DurationSeconds != 0 {
		return MediaInput{}, errors.New("media duration is not supported")
	}
	if spec.requiresThumbnail {
		if input.ThumbnailRef == "" || !validMediaFileName(input.ThumbnailFileName) {
			return MediaInput{}, errors.New("video thumbnail reference and file name are required")
		}
	} else if input.ThumbnailRef != "" || input.ThumbnailFileName != "" {
		return MediaInput{}, errors.New("media thumbnail is not supported")
	}
	return input, nil
}

func validMediaFileName(name string) bool {
	if name == "" || len(name) > maxMediaFileNameBytes || !utf8.ValidString(name) || strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	return filepath.Base(name) == name && name != "." && name != ".."
}
