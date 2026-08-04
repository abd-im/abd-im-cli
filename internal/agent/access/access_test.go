package access

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/testkit"
)

func TestEnvironmentRoundTripReplacesSensitiveRunValues(t *testing.T) {
	values := Context{
		Socket: filepath.Join(t.TempDir(), "agent.sock"), ProfileID: "work", RunID: "run-1",
		Grant: "grant-1", AllowedMethods: []string{"message.history"},
	}
	environment, err := Environment([]string{"PATH=/usr/bin", EnvGrant + "=stale", "HOME=/home/test"}, "/opt/abdim/bin/abdim", values)
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(key string) string {
		for _, entry := range environment {
			if strings.HasPrefix(entry, key+"=") {
				return strings.TrimPrefix(entry, key+"=")
			}
		}
		return ""
	}
	parsed, present, err := FromEnvironment(lookup)
	if err != nil || !present || parsed.Socket != values.Socket || parsed.Grant != values.Grant || len(parsed.AllowedMethods) != 1 {
		t.Fatalf("FromEnvironment() = %#v, %t, %v", parsed, present, err)
	}
	if path := lookup("PATH"); !strings.HasPrefix(path, "/opt/abdim/bin"+string(os.PathListSeparator)) {
		t.Fatalf("PATH = %q", path)
	}
}

func TestFromEnvironmentRejectsPartialContext(t *testing.T) {
	_, present, err := FromEnvironment(func(key string) string {
		if key == EnvSocket {
			return "/tmp/agent.sock"
		}
		return ""
	})
	if !present || err == nil {
		t.Fatalf("FromEnvironment() present=%t err=%v", present, err)
	}
}

func TestListenForwardsFramedRequestToRunProxy(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "agent.sock")
	responseValue := contracts.Response{
		APIVersion: contracts.APIVersionV1, RequestID: "request-1", OK: true,
		Data: json.RawMessage(`{"items":[]}`), Meta: &contracts.Meta{ProfileID: "work"},
	}
	proxy := &testkit.FakeProxy{Response: &responseValue}
	server, err := Listen(socket, proxy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	response, err := ipc.Call(context.Background(), socket, contracts.Request{
		APIVersion: contracts.APIVersionV1, RequestID: "request-1", ProfileID: "work",
		Method: "message.history", Params: json.RawMessage(`{}`), Grant: "grant-1",
	})
	if err != nil || !response.OK {
		t.Fatalf("Call() = %#v, %v", response, err)
	}
	if calls := proxy.Calls(); len(calls) != 1 || calls[0].Grant != "grant-1" {
		t.Fatalf("proxy calls = %+v", calls)
	}
}
