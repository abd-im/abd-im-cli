package message

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/profile"
)

var (
	ErrAttachmentAccess  = errors.New("attachment does not belong to the grant")
	ErrAttachmentExpired = errors.New("attachment has expired")
)

// AttachmentKind identifies the fixed media category expected by a future
// message action. The reference itself has no filename or local path.
type AttachmentKind string

const (
	AttachmentImage AttachmentKind = "image"
	AttachmentFile  AttachmentKind = "file"
	AttachmentSound AttachmentKind = "sound"
	AttachmentVideo AttachmentKind = "video"
)

// AttachmentStore owns the private files behind opaque attachment references.
// It has no provider-facing path API.
type AttachmentStore struct {
	control *control.Store
	paths   profile.Paths
	mu      sync.Mutex
}

func NewAttachmentStore(store *control.Store, paths profile.Paths) (*AttachmentStore, error) {
	if store == nil || strings.TrimSpace(paths.ProfileID) == "" || strings.TrimSpace(paths.AttachmentsDir) == "" {
		return nil, errors.New("control store, profile ID, and attachments directory are required")
	}
	if err := paths.EnsurePrivate(); err != nil {
		return nil, err
	}
	return &AttachmentStore{control: store, paths: paths}, nil
}

// Put writes an attachment from a trusted stream. Callers cannot supply a
// filename or path, and the stored reference is bound to the issuing run.
func (s *AttachmentStore) Put(ctx context.Context, item grant.Grant, kind AttachmentKind, reader io.Reader) (control.Attachment, error) {
	if reader == nil {
		return control.Attachment{}, errors.New("attachment reader is required")
	}
	if err := s.validateGrant(item); err != nil {
		return control.Attachment{}, err
	}
	if !validAttachmentKind(kind) {
		return control.Attachment{}, errors.New("invalid attachment kind")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	reference, err := newAttachmentReference()
	if err != nil {
		return control.Attachment{}, err
	}
	path, err := s.paths.AttachmentPath(reference)
	if err != nil {
		return control.Attachment{}, err
	}
	temporary, err := os.CreateTemp(s.paths.AttachmentsDir, ".attachment-*")
	if err != nil {
		return control.Attachment{}, fmt.Errorf("create attachment: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return control.Attachment{}, fmt.Errorf("secure attachment: %w", err)
	}

	maximum := item.AttachmentByteLimit
	if maximum < math.MaxInt64 {
		maximum++
	}
	size, copyErr := io.Copy(temporary, io.LimitReader(reader, maximum))
	if closeErr := temporary.Close(); copyErr != nil {
		return control.Attachment{}, fmt.Errorf("write attachment: %w", copyErr)
	} else if closeErr != nil {
		return control.Attachment{}, fmt.Errorf("close attachment: %w", closeErr)
	}
	if size > item.AttachmentByteLimit {
		return control.Attachment{}, control.ErrAttachmentQuota
	}

	attachment := control.Attachment{
		ID:        reference,
		ProfileID: item.ProfileID,
		RunID:     item.RunID,
		GrantID:   item.ID,
		Kind:      string(kind),
		SizeBytes: size,
		ByteLimit: item.AttachmentByteLimit,
		ExpiresAt: item.ExpiresAt,
		CreatedAt: time.Now(),
	}
	if err := s.control.PutAttachment(ctx, attachment); err != nil {
		return control.Attachment{}, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		if removeErr := s.control.DeleteAttachment(ctx, attachment.ProfileID, attachment.ID); removeErr != nil {
			return control.Attachment{}, fmt.Errorf("publish attachment: %w; remove reservation: %v", err, removeErr)
		}
		return control.Attachment{}, fmt.Errorf("publish attachment: %w", err)
	}
	return attachment, nil
}

// Open resolves an opaque reference only for the profile and run that created
// it. Callers receive a file handle rather than its local path.
func (s *AttachmentStore) Open(ctx context.Context, item grant.Grant, reference string, kind AttachmentKind) (*os.File, control.Attachment, error) {
	if err := s.validateGrant(item); err != nil {
		return nil, control.Attachment{}, err
	}
	if !validAttachmentKind(kind) {
		return nil, control.Attachment{}, errors.New("invalid attachment kind")
	}
	attachment, err := s.control.AttachmentByID(ctx, item.ProfileID, reference)
	if err != nil {
		return nil, control.Attachment{}, err
	}
	if attachment.RunID != item.RunID || attachment.GrantID != item.ID || attachment.ByteLimit != item.AttachmentByteLimit || attachment.Kind != string(kind) {
		return nil, control.Attachment{}, ErrAttachmentAccess
	}
	if !time.Now().Before(attachment.ExpiresAt) {
		return nil, control.Attachment{}, ErrAttachmentExpired
	}
	path, err := s.paths.AttachmentPath(reference)
	if err != nil {
		return nil, control.Attachment{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, control.Attachment{}, fmt.Errorf("open attachment: %w", err)
	}
	return file, attachment, nil
}

func (s *AttachmentStore) validateGrant(item grant.Grant) error {
	if item.ProfileID != s.paths.ProfileID || strings.TrimSpace(item.RunID) == "" {
		return ErrAttachmentAccess
	}
	if item.AttachmentByteLimit <= 0 {
		return control.ErrAttachmentQuota
	}
	if item.ExpiresAt.IsZero() || !time.Now().Before(item.ExpiresAt) {
		return ErrAttachmentExpired
	}
	return nil
}

func validAttachmentKind(kind AttachmentKind) bool {
	switch kind {
	case AttachmentImage, AttachmentFile, AttachmentSound, AttachmentVideo:
		return true
	default:
		return false
	}
}

func newAttachmentReference() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate attachment reference: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
