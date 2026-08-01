//go:build integration

package friend

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

// TestOpenIMFriendLifecycleIntegration uses a disposable two-user fixture. It
// leaves the users without a friendship and rejects a pending request during
// cleanup when the test fails part-way through.
func TestOpenIMFriendLifecycleIntegration(t *testing.T) {
	apiAddr := friendIntegrationEnv(t, "ABDIM_OPENIM_API_ADDR")
	requesterID := friendIntegrationEnv(t, "ABDIM_OPENIM_FRIEND_REQUESTER_ID")
	requesterToken := friendIntegrationEnv(t, "ABDIM_OPENIM_FRIEND_REQUESTER_TOKEN")
	responderID := friendIntegrationEnv(t, "ABDIM_OPENIM_FRIEND_RESPONDER_ID")
	responderToken := friendIntegrationEnv(t, "ABDIM_OPENIM_FRIEND_RESPONDER_TOKEN")
	if requesterID == responderID {
		t.Fatal("friend lifecycle fixture requires two different users")
	}

	requester := newIntegrationActions(apiAddr, requesterID, requesterToken)
	responder := newIntegrationActions(apiAddr, responderID, responderToken)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if exists, err := requester.HasFriend(ctx, responderID); err != nil {
		t.Fatalf("initial HasFriend() error = %v", err)
	} else if exists {
		if err := requester.DeleteFriend(ctx, DeleteInput{UserID: responderID}); err != nil {
			t.Fatalf("reset existing friendship: %v", err)
		}
		if err := waitFriendState(ctx, requester, responderID, false); err != nil {
			t.Fatalf("wait for friendship reset: %v", err)
		}
	}
	if pending, err := responder.HasPendingRequest(ctx, requesterID); err != nil {
		t.Fatalf("initial HasPendingRequest() error = %v", err)
	} else if pending {
		if err := responder.RespondFriend(ctx, RespondInput{UserID: requesterID, Response: "reject", Message: "integration reset"}); err != nil {
			t.Fatalf("reset existing pending request: %v", err)
		}
	}

	cleanup := true
	defer func() {
		if !cleanup {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if pending, _ := responder.HasPendingRequest(cleanupCtx, requesterID); pending {
			_ = responder.RespondFriend(cleanupCtx, RespondInput{UserID: requesterID, Response: "reject", Message: "integration cleanup"})
		}
		if exists, _ := requester.HasFriend(cleanupCtx, responderID); exists {
			_ = requester.DeleteFriend(cleanupCtx, DeleteInput{UserID: responderID})
		}
	}()

	if err := requester.RequestFriend(ctx, RequestInput{UserID: responderID, Message: "abdim friend integration"}); err != nil {
		t.Fatalf("RequestFriend() error = %v", err)
	}
	if pending, err := responder.HasPendingRequest(ctx, requesterID); err != nil || !pending {
		t.Fatalf("pending request = %t, %v", pending, err)
	}
	if err := responder.RespondFriend(ctx, RespondInput{UserID: requesterID, Response: "accept", Message: "accepted"}); err != nil {
		t.Fatalf("RespondFriend() error = %v", err)
	}
	if err := requester.SetFriendRemark(ctx, SetRemarkInput{UserID: responderID, Remark: "abdim-integration"}); err != nil {
		t.Fatalf("SetFriendRemark() error = %v", err)
	}
	if exists, err := requester.HasFriend(ctx, responderID); err != nil || !exists {
		t.Fatalf("created friendship = %t, %v", exists, err)
	}
	if err := requester.DeleteFriend(ctx, DeleteInput{UserID: responderID}); err != nil {
		t.Fatalf("DeleteFriend() error = %v", err)
	}
	if exists, err := requester.HasFriend(ctx, responderID); err != nil || exists {
		t.Fatalf("deleted friendship = %t, %v", exists, err)
	}
	cleanup = false
}

func waitFriendState(ctx context.Context, actions *OpenIMActions, userID string, want bool) error {
	for {
		exists, err := actions.HasFriend(ctx, userID)
		if err != nil {
			return err
		}
		if exists == want {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func newIntegrationActions(apiAddr, userID, token string) *OpenIMActions {
	actions, err := NewOpenIMActions(OpenIMActions{Context: func() context.Context {
		return ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
			UserID:   userID,
			Token:    token,
			IMConfig: &sdk_struct.IMConfig{ApiAddr: apiAddr},
		})
	}})
	if err != nil {
		panic(err)
	}
	return actions
}

func friendIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for integration tests", name)
	}
	return value
}
