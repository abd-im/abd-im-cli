package e2e

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	messagecapability "github.com/abd-im/abd-im-cli/internal/capability/message"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/profile"
)

func TestAttachmentReferenceCannotCrossRunE2E(t *testing.T) {
	ctx := context.Background()
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
	t.Cleanup(func() { _ = store.Close() })
	attachments, err := messagecapability.NewAttachmentStore(store, paths)
	if err != nil {
		t.Fatal(err)
	}
	owner := e2eAttachmentGrant(t, "run-owner")
	attachment, err := attachments.Put(ctx, owner, messagecapability.AttachmentFile, strings.NewReader("private"))
	if err != nil {
		t.Fatal(err)
	}
	otherRun := e2eAttachmentGrant(t, "run-other")
	if _, _, err := attachments.Open(ctx, otherRun, attachment.ID, messagecapability.AttachmentFile); !errors.Is(err, messagecapability.ErrAttachmentAccess) {
		t.Fatalf("Open(other run) error = %v, want ErrAttachmentAccess", err)
	}
	if _, err := attachments.Put(ctx, owner, messagecapability.AttachmentFile, strings.NewReader("four")); !errors.Is(err, control.ErrAttachmentQuota) {
		t.Fatalf("Put(over quota) error = %v, want ErrAttachmentQuota", err)
	}
}

func e2eAttachmentGrant(t *testing.T, runID string) grant.Grant {
	t.Helper()
	issued, _, err := grant.NewStore().Issue(grant.Policy{
		RunID: runID, ProfileID: "work", Principal: "provider",
		Methods: []string{"message.send_file"}, Scopes: []string{"message.send_file"},
		ExpiresAt: time.Now().Add(time.Hour), RateBudget: 2, AttachmentByteLimit: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	return issued
}
