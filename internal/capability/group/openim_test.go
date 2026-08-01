package group

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/operation"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/constant"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	pbgroup "github.com/openimsdk/protocol/group"
)

func TestOpenIMCreatorPostsAuthenticatedServerAction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/group/create_group" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("token") != "secret-token" || request.Header.Get("operationID") == "" {
			t.Fatal("group create request is not authenticated")
		}
		var input pbgroup.CreateGroupReq
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if input.OwnerUserID != "owner-1" || !reflect.DeepEqual(input.MemberUserIDs, []string{"member-1", "member-2"}) || input.GroupInfo == nil || input.GroupInfo.GroupName != "project" || input.GroupInfo.GroupType != constant.WorkingGroup || len(input.AdminUserIDs) != 0 || input.SendMessage != nil {
			t.Fatalf("group create input owner=%q members=%v group=%v admins=%v send_message=%v", input.OwnerUserID, input.MemberUserIDs, input.GroupInfo, input.AdminUserIDs, input.SendMessage)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0})
	}))
	defer server.Close()

	creator, err := NewOpenIMCreator(OpenIMCreator{Context: testOpenIMContext(server.URL), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateGroup(context.Background(), Input{Name: "project", MemberIDs: []string{"member-1", "member-2"}}); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
}

func TestOpenIMCreatorPreservesUnknownOutcomes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	creator, err := NewOpenIMCreator(OpenIMCreator{Context: testOpenIMContext(server.URL), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateGroup(context.Background(), Input{Name: "project", MemberIDs: []string{"member-1"}}); !errors.Is(err, operation.ErrOutcomeUnknown) {
		t.Fatalf("CreateGroup() error = %v, want unknown outcome", err)
	}
}

func TestOpenIMCreatorRedactsServerRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 1001, "errMsg": "secret-token"})
	}))
	defer server.Close()
	creator, err := NewOpenIMCreator(OpenIMCreator{Context: testOpenIMContext(server.URL), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	err = creator.CreateGroup(context.Background(), Input{Name: "project", MemberIDs: []string{"member-1"}})
	if err == nil || errors.Is(err, operation.ErrOutcomeUnknown) || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("CreateGroup() error = %v", err)
	}
}

func TestNewOpenIMCreatorRequiresContext(t *testing.T) {
	if _, err := NewOpenIMCreator(OpenIMCreator{}); err == nil {
		t.Fatal("NewOpenIMCreator() error = nil")
	}
}

func testOpenIMContext(apiAddr string) func() context.Context {
	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID:   "owner-1",
		Token:    "secret-token",
		IMConfig: &sdk_struct.IMConfig{ApiAddr: apiAddr},
	})
	return func() context.Context { return base }
}
