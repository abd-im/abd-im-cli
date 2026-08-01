package blacklist

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/operation"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	"github.com/openimsdk/protocol/relation"
	"github.com/openimsdk/protocol/sdkws"
)

func TestOpenIMSourceUsesFixedAuthenticatedBlacklistEndpoints(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		if request.Method != http.MethodPost || request.Header.Get("token") != "secret-token" || request.Header.Get("operationID") == "" {
			t.Fatalf("request = %s %s token=%q operation=%q", request.Method, request.URL.Path, request.Header.Get("token"), request.Header.Get("operationID"))
		}
		switch request.URL.Path {
		case "/friend/add_black":
			var input relation.AddBlackReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.OwnerUserID != "owner-1" || input.BlackUserID != "user-2" || input.Ex != "" {
				t.Fatalf("add input owner=%q user=%q ex=%q", input.OwnerUserID, input.BlackUserID, input.Ex)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0})
		case "/friend/get_specified_blacks":
			var input relation.GetSpecifiedBlacksReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.OwnerUserID != "owner-1" || len(input.UserIDList) != 1 || input.UserIDList[0] != "user-2" {
				t.Fatalf("blacklist verification input owner=%q users=%v", input.OwnerUserID, input.UserIDList)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0, "data": &relation.GetSpecifiedBlacksResp{Blacks: []*sdkws.BlackInfo{{BlackUserInfo: &sdkws.PublicUserInfo{UserID: "user-2"}}}}})
		case "/friend/remove_black":
			var input relation.RemoveBlackReq
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input.OwnerUserID != "owner-1" || input.BlackUserID != "user-2" {
				t.Fatalf("remove input owner=%q user=%q", input.OwnerUserID, input.BlackUserID)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	source, err := NewOpenIMSource(OpenIMSource{Context: testOpenIMContext(server.URL), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.AddBlacklist(context.Background(), "user-2"); err != nil {
		t.Fatalf("AddBlacklist() error = %v", err)
	}
	blocked, err := source.IsBlacklisted(context.Background(), "user-2")
	if err != nil || !blocked {
		t.Fatalf("IsBlacklisted() = %t, %v", blocked, err)
	}
	if err := source.RemoveBlacklist(context.Background(), "user-2"); err != nil {
		t.Fatalf("RemoveBlacklist() error = %v", err)
	}
	if want := []string{"/friend/add_black", "/friend/get_specified_blacks", "/friend/remove_black"}; strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestOpenIMSourceReportsMissingRelationshipAndRedactsErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/friend/get_specified_blacks" {
			_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0, "data": &relation.GetSpecifiedBlacksResp{}})
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 1001, "errMsg": "secret-token"})
	}))
	defer server.Close()
	source, err := NewOpenIMSource(OpenIMSource{Context: testOpenIMContext(server.URL), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if blocked, err := source.IsBlacklisted(context.Background(), "user-2"); err != nil || blocked {
		t.Fatalf("IsBlacklisted() = %t, %v", blocked, err)
	}
	if err := source.AddBlacklist(context.Background(), "user-2"); err == nil || errors.Is(err, operation.ErrOutcomeUnknown) || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("AddBlacklist() rejection = %v", err)
	}
}

func TestOpenIMSourcePreservesUnknownActionOutcome(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	source, err := NewOpenIMSource(OpenIMSource{Context: testOpenIMContext(server.URL), HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.RemoveBlacklist(context.Background(), "user-2"); !errors.Is(err, operation.ErrOutcomeUnknown) {
		t.Fatalf("RemoveBlacklist() error = %v", err)
	}
}

func TestNewOpenIMSourceRequiresContext(t *testing.T) {
	if _, err := NewOpenIMSource(OpenIMSource{}); err == nil {
		t.Fatal("NewOpenIMSource() error = nil")
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
