package profile

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
	"github.com/openimsdk/protocol/sdkws"
	pbuser "github.com/openimsdk/protocol/user"
)

func TestOpenIMSourceUsesFixedServerUserReadAndRuntimeFacts(t *testing.T) {
	client := &fakeClient{users: []User{{ID: "user-1", Name: "Alice", Nickname: "Alice", Avatar: "https://example.test/a.png"}}}
	source, err := NewOpenIMSource(OpenIMSourceConfig{
		Profile: Profile{ID: "work", Name: "Work", SDKVersion: "sdk-test"},
		SelfID:  "user-1",
		Client:  client,
		Daemon: func() DaemonStatus {
			return DaemonStatus{ProfileID: "work", State: "ready", CredentialsValid: true}
		},
	})
	if err != nil {
		t.Fatalf("NewOpenIMSource() error = %v", err)
	}

	profile, err := source.Profile(context.Background())
	if err != nil || profile.Name != "Work" {
		t.Fatalf("Profile() = %#v, %v", profile, err)
	}
	self, err := source.Self(context.Background())
	if err != nil || self.ID != "user-1" || !reflect.DeepEqual(client.ids, []string{"user-1"}) {
		t.Fatalf("Self() = %#v, %v; IDs = %v", self, err, client.ids)
	}
	users, err := source.Users(context.Background(), []string{"user-1"})
	if err != nil || len(users) != 1 || users[0].Nickname != "Alice" {
		t.Fatalf("Users() = %#v, %v", users, err)
	}
	status, err := source.Daemon(context.Background())
	if err != nil || status.State != "ready" {
		t.Fatalf("Daemon() = %#v, %v", status, err)
	}
	report, err := source.Doctor(context.Background())
	if err != nil || !report.OK || len(report.Checks) != 2 {
		t.Fatalf("Doctor() = %#v, %v", report, err)
	}
}

func TestOpenIMSourceDoctorReportsServerFailuresWithoutDetails(t *testing.T) {
	source, err := NewOpenIMSource(OpenIMSourceConfig{
		Profile: Profile{ID: "work"},
		SelfID:  "user-1",
		Client:  &fakeClient{err: errors.New("token-marker")},
		Daemon: func() DaemonStatus {
			return DaemonStatus{ProfileID: "work", State: "ready", CredentialsValid: true}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := source.Doctor(context.Background())
	if err != nil || report.OK || len(report.Checks) != 2 || report.Checks[1].Status != "failed" || strings.Contains(report.Checks[1].Detail, "token-marker") {
		t.Fatalf("Doctor() = %#v, %v", report, err)
	}
}

func TestOpenIMClientPostsAuthenticatedUserRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/user/get_users_info" || request.Header.Get("token") != "secret-token" || request.Header.Get("operationID") == "" {
			t.Errorf("unexpected user request %s token=%q operation=%q", request.URL.Path, request.Header.Get("token"), request.Header.Get("operationID"))
		}
		defer request.Body.Close()
		var input pbuser.GetDesignateUsersReq
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil || !reflect.DeepEqual(input.UserIDs, []string{"user-1"}) {
			t.Errorf("user request IDs = %v, %v", input.UserIDs, err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 0, "data": &pbuser.GetDesignateUsersResp{UsersInfo: []*sdkws.UserInfo{{UserID: "user-1", Nickname: "Alice", FaceURL: "https://example.test/a.png"}}}})
	}))
	defer server.Close()

	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{
		UserID:   "user-1",
		Token:    "secret-token",
		IMConfig: &sdk_struct.IMConfig{ApiAddr: server.URL},
	})
	users, err := (OpenIMClient{Context: func() context.Context { return base }, HTTPClient: server.Client()}).Users(context.Background(), []string{"user-1"})
	if err != nil || !reflect.DeepEqual(users, []User{{ID: "user-1", Name: "Alice", Nickname: "Alice", Avatar: "https://example.test/a.png"}}) {
		t.Fatalf("Users() = %#v, %v", users, err)
	}
}

func TestOpenIMClientRedactsServerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"errCode": 1001, "errMsg": "secret-token"})
	}))
	defer server.Close()
	base := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{UserID: "user-1", Token: "secret-token", IMConfig: &sdk_struct.IMConfig{ApiAddr: server.URL}})
	_, err := (OpenIMClient{Context: func() context.Context { return base }, HTTPClient: server.Client()}).Users(context.Background(), []string{"user-1"})
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("Users() error = %v", err)
	}
}

type fakeClient struct {
	users []User
	ids   []string
	err   error
}

func (c *fakeClient) Users(_ context.Context, ids []string) ([]User, error) {
	c.ids = append([]string(nil), ids...)
	if c.err != nil {
		return nil, c.err
	}
	return append([]User(nil), c.users...), nil
}
