package message

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/profile"
)

func TestAttachmentStoreBindsOpaqueReferencesToProfileRunQuotaAndExpiry(t *testing.T) {
	ctx := context.Background()
	paths, err := profile.NewPaths(filepath.Join(t.TempDir(), "config"), t.TempDir(), t.TempDir(), "work")
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsurePrivate(); err != nil {
		t.Fatal(err)
	}
	controlStore, err := control.Open(paths.ControlDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controlStore.Close() })
	attachments, err := NewAttachmentStore(controlStore, paths)
	if err != nil {
		t.Fatal(err)
	}
	access := attachmentGrant(t, "work", "run-1", 8, time.Now().Add(time.Hour))
	attachment, err := attachments.Put(ctx, access, AttachmentImage, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if attachment.ID == "" || attachment.ProfileID != "work" || attachment.RunID != "run-1" || attachment.SizeBytes != 5 || attachment.ExpiresAt != access.ExpiresAt {
		t.Fatalf("attachment = %+v", attachment)
	}
	path, err := paths.AttachmentPath(attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat attachment: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("attachment mode = %o, want 600", info.Mode().Perm())
	}
	file, metadata, err := attachments.Open(ctx, access, attachment.ID, AttachmentImage)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	contents, err := io.ReadAll(file)
	if closeErr := file.Close(); err != nil {
		t.Fatal(err)
	} else if closeErr != nil {
		t.Fatal(closeErr)
	}
	if string(contents) != "hello" || metadata.ID != attachment.ID {
		t.Fatalf("Open() = %q, %+v", contents, metadata)
	}

	wrongRun := access
	wrongRun.RunID = "run-2"
	if _, _, err := attachments.Open(ctx, wrongRun, attachment.ID, AttachmentImage); !errors.Is(err, ErrAttachmentAccess) {
		t.Fatalf("Open(wrong run) error = %v, want ErrAttachmentAccess", err)
	}
	otherGrant := attachmentGrant(t, "work", "run-1", 8, time.Now().Add(time.Hour))
	if _, _, err := attachments.Open(ctx, otherGrant, attachment.ID, AttachmentImage); !errors.Is(err, ErrAttachmentAccess) {
		t.Fatalf("Open(other grant) error = %v, want ErrAttachmentAccess", err)
	}
	wrongProfile := access
	wrongProfile.ProfileID = "other"
	if _, _, err := attachments.Open(ctx, wrongProfile, attachment.ID, AttachmentImage); !errors.Is(err, ErrAttachmentAccess) {
		t.Fatalf("Open(wrong profile) error = %v, want ErrAttachmentAccess", err)
	}
	expired := access
	expired.ExpiresAt = time.Now().Add(-time.Second)
	if _, _, err := attachments.Open(ctx, expired, attachment.ID, AttachmentImage); !errors.Is(err, ErrAttachmentExpired) {
		t.Fatalf("Open(expired) error = %v, want ErrAttachmentExpired", err)
	}
	if _, err := attachments.Put(ctx, access, AttachmentImage, strings.NewReader("more")); !errors.Is(err, control.ErrAttachmentQuota) {
		t.Fatalf("Put(over quota) error = %v, want ErrAttachmentQuota", err)
	}
}

func TestAttachmentStoreRejectsUntrustedPathsAndKinds(t *testing.T) {
	ctx := context.Background()
	paths, err := profile.NewPaths(t.TempDir(), t.TempDir(), t.TempDir(), "work")
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsurePrivate(); err != nil {
		t.Fatal(err)
	}
	controlStore, err := control.Open(paths.ControlDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controlStore.Close() })
	attachments, err := NewAttachmentStore(controlStore, paths)
	if err != nil {
		t.Fatal(err)
	}
	access := attachmentGrant(t, "work", "run-1", 8, time.Now().Add(time.Hour))
	if _, err := attachments.Put(ctx, access, AttachmentKind("../../file"), strings.NewReader("data")); err == nil {
		t.Fatal("Put() accepted an invalid attachment kind")
	}
	if _, _, err := attachments.Open(ctx, access, "../../file", AttachmentImage); err == nil {
		t.Fatal("Open() accepted a path-like reference")
	}
}

func attachmentGrant(t *testing.T, profileID, runID string, limit int64, expiresAt time.Time) grant.Grant {
	t.Helper()
	issued, _, err := grant.NewStore().Issue(grant.Policy{
		RunID: runID, ProfileID: profileID, Principal: "provider",
		Methods: []string{"message.send_image"}, Scopes: []string{"message.send_image"},
		ExpiresAt: expiresAt, RateBudget: 4, AttachmentByteLimit: limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	return issued
}
