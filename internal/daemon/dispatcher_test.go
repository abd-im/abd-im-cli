package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

func TestDispatcherRoutesBySDKIdentity(t *testing.T) {
	method := func(identity string) Method {
		return Method{Name: "user.me", Handle: func(context.Context, json.RawMessage) (MethodResult, error) {
			return MethodResult{Data: map[string]string{"identity": identity}, Meta: contracts.Meta{ProfileID: "work"}}, nil
		}}
	}
	dispatcher, err := NewDispatcher("work", []Method{method("user")}, []Method{method("bot")})
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range []string{"user", "bot"} {
		response, err := dispatcher.Handle(context.Background(), contracts.Request{
			APIVersion: contracts.APIVersionV1, RequestID: identity, ProfileID: "work", As: identity,
			Method: "user.me", Params: json.RawMessage(`{}`),
		})
		if err != nil || !response.OK || string(response.Data) != `{"identity":"`+identity+`"}` {
			t.Fatalf("Handle(%s) = %#v, %v", identity, response, err)
		}
	}
}
