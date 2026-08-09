package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/access"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/daemon"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/profile"
)

func TestLegacyManualSetupCommandsAreRemoved(t *testing.T) {
	for _, args := range [][]string{
		{"auth", "import", "--token-stdin"},
		{"profile", "configure"},
		{"daemon", "verify"},
		{"daemon", "serve"},
	} {
		var output bytes.Buffer
		if got := runWithIO(args, strings.NewReader("secret-marker"), &output, testRoots(t)); got != 2 {
			t.Fatalf("runWithIO(%v) = %d, want 2", args, got)
		}
		if strings.Contains(output.String(), "secret-marker") {
			t.Fatalf("legacy command leaked input: %s", output.String())
		}
	}
}

func TestDirectMessagePolicyDefaultsToReplyOnlyAndBindsSender(t *testing.T) {
	policy := directMessagePolicy("bot-user", false, nil)
	for _, sender := range []string{"user-1", "user-2"} {
		decision, allowed, err := policy.Decide(context.Background(), daemon.InboundContext{SenderID: sender, SessionType: 1})
		if err != nil || !allowed || decision.Principal != "openim:"+sender || len(decision.Methods) != 0 || decision.RateBudget != 1 || decision.AttachmentByteLimit != 0 {
			t.Fatalf("inbound sender %q policy = %+v, allowed=%t, err=%v", sender, decision, allowed, err)
		}
	}
	for name, inbound := range map[string]daemon.InboundContext{
		"self":  {SenderID: "bot-user", SessionType: 1},
		"group": {SenderID: "user-1", GroupID: "group-1", SessionType: 2},
	} {
		if _, allowed, err := policy.Decide(context.Background(), inbound); err != nil || allowed {
			t.Fatalf("%s policy allowed=%t err=%v", name, allowed, err)
		}
	}
}

func TestDirectMessagePolicyExplicitlyEnablesRegisteredTools(t *testing.T) {
	methods := []proxy.Method{{Name: "message.history"}, {Name: "message.send_text"}}
	policy := directMessagePolicy("bot-user", true, methods)
	decision, allowed, err := policy.Decide(context.Background(), daemon.InboundContext{SenderID: "user-1", SessionType: 1})
	if err != nil || !allowed {
		t.Fatalf("enabled policy allowed=%t err=%v", allowed, err)
	}
	if decision.Principal != "openim:user-1" || decision.RateBudget != 64 || decision.AttachmentByteLimit != 32*1024*1024 || !decision.HistoryBeforeTrigger {
		t.Fatalf("enabled policy = %+v", decision)
	}
	if len(decision.Methods) != len(methods) {
		t.Fatalf("enabled methods = %v", decision.Methods)
	}
}

func TestAgentWorkspacePolicyAllowsUserContentButNotAgentStream(t *testing.T) {
	policy := agentWorkspacePolicy(directMessagePolicy("agent-1", false, nil), "agent-1", false, nil)
	decision, allowed, err := policy.Decide(context.Background(), daemon.InboundContext{
		SenderID: "user-1", SenderPlatformID: 5, GroupID: "group-1", SessionType: 3, ContentType: 101, ConversationKind: contracts.ConversationKindAgentWorkspace,
	})
	if err != nil || !allowed || decision.Principal != "openim:user-1" {
		t.Fatalf("workspace policy = %+v, allowed=%t, err=%v", decision, allowed, err)
	}
	if _, allowed, err := policy.Decide(context.Background(), daemon.InboundContext{
		SenderID: "user-1", SenderPlatformID: 5, GroupID: "group-1", SessionType: 3, ContentType: 143, ConversationKind: contracts.ConversationKindAgentWorkspace,
	}); err != nil || allowed {
		t.Fatalf("workspace stream allowed=%t err=%v", allowed, err)
	}
	if _, allowed, err := policy.Decide(context.Background(), daemon.InboundContext{
		SenderID: "user-1", SenderPlatformID: 5, GroupID: "group-1", SessionType: 3, ContentType: 102, ConversationKind: contracts.ConversationKindAgentWorkspace,
	}); err != nil || allowed {
		t.Fatalf("workspace picture allowed=%t err=%v", allowed, err)
	}
	if _, allowed, err := policy.Decide(context.Background(), daemon.InboundContext{
		SenderID: "agent-1", SenderPlatformID: 5, GroupID: "group-1", SessionType: 3, ContentType: 101, ConversationKind: contracts.ConversationKindAgentWorkspace,
	}); err != nil || allowed {
		t.Fatalf("agent-authored workspace message allowed=%t err=%v", allowed, err)
	}
}

func TestDaemonSDKConfigUsesProfilePaths(t *testing.T) {
	paths, err := profile.NewPaths("/config", "/data", "/runtime", "work")
	if err != nil {
		t.Fatal(err)
	}
	config := daemonSDKConfig(paths, profile.Deployment{UserID: "user-1", APIAddr: "https://2.example.test/api", ChatAPIAddr: "https://2.example.test/chat", WSAddr: "wss://2.example.test/msg_gateway", PlatformID: 7})
	if config.SystemType != runtime.GOOS || config.PlatformID != 7 || config.ApiAddr != "https://2.example.test/api" || config.WsAddr != "wss://2.example.test/msg_gateway" || config.DataDir != paths.SDKDir || config.LogFilePath != filepath.Join(paths.LogsDir, "sdk.log") {
		t.Fatalf("daemonSDKConfig() = %#v", config)
	}
}

func TestAgentLaunchUsesFixedCommands(t *testing.T) {
	tests := map[string]agentLaunchSpec{
		"codex":    {command: "codex"},
		"hermes":   {command: "hermes", args: []string{"acp"}},
		"openclaw": {command: "openclaw", args: []string{"acp"}},
	}
	for providerID, want := range tests {
		got, err := agentLaunch(providerID)
		if err != nil || got.command != want.command || strings.Join(got.args, " ") != strings.Join(want.args, " ") {
			t.Errorf("agentLaunch(%q) = %#v, %v", providerID, got, err)
		}
	}
	if _, err := agentLaunch("/tmp/custom-agent"); err == nil {
		t.Fatal("agentLaunch accepted arbitrary executable")
	}
}

func TestOwnerQueryUsesOnlyRegisteredMethodOverLocalSocket(t *testing.T) {
	roots := testRoots(t)
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, "work")
	if err != nil {
		t.Fatalf("NewPaths() error = %v", err)
	}
	requests := make(chan contracts.Request, 1)
	server, err := ipc.Listen(paths.Socket, func(_ context.Context, request contracts.Request) (contracts.Response, error) {
		requests <- request
		return contracts.Response{
			APIVersion: contracts.APIVersionV1,
			RequestID:  request.RequestID,
			OK:         true,
			Data:       json.RawMessage(`{"items":[]}`),
			Meta:       &contracts.Meta{ProfileID: "work", Schema: "abdim.service/v1"},
		}, nil
	})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Error("Serve() did not stop")
		}
	})

	var output bytes.Buffer
	args := []string{"--profile", "work", "group", "members", "list", "--params-stdin"}
	if got := runWithIO(args, strings.NewReader(`{"group_id":"group-1","limit":20}`), &output, roots); got != 0 {
		t.Fatalf("runWithIO() = %d, want 0; output = %s", got, output.String())
	}
	var response contracts.Response
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode CLI response: %v", err)
	}
	if !response.OK || string(response.Data) != `{"items":[]}` || response.Meta == nil || response.Meta.ProfileID != "work" {
		t.Fatalf("CLI response = %+v", response)
	}
	request := <-requests
	if request.Method != "group.members.list" || string(request.Params) != `{"group_id":"group-1","limit":20}` {
		t.Fatalf("daemon request = %+v", request)
	}
}

func TestOwnerQueryRejectsUnregisteredCommandsAndDaemonPaths(t *testing.T) {
	roots := testRoots(t)
	var output bytes.Buffer
	if got := runWithIO([]string{"--profile", "work", "daemon", "shutdown"}, strings.NewReader(""), &output, roots); got != 2 {
		t.Fatalf("unregistered command exit = %d, want 2", got)
	}
	var response contracts.Response
	if err := json.Unmarshal(output.Bytes(), &response); err != nil || response.Error == nil || response.Error.Code != contracts.CodeInvalidArgument {
		t.Fatalf("unregistered command response = %s, %v", output.String(), err)
	}

	output.Reset()
	if got := runWithIO([]string{"--profile", "work", "profile", "get"}, strings.NewReader(""), &output, roots); got != 3 {
		t.Fatalf("unavailable daemon exit = %d, want 3", got)
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil || response.Error == nil || response.Error.Code != contracts.CodeDaemonUnavailable || strings.Contains(output.String(), roots.runtimeDir) {
		t.Fatalf("daemon unavailable response = %s, %v", output.String(), err)
	}
}

func TestCommandsListsDefaultOwnerRegistry(t *testing.T) {
	roots := testRoots(t)
	var output bytes.Buffer
	if got := runWithIO([]string{"--profile", "work", "commands"}, strings.NewReader(""), &output, roots); got != 0 {
		t.Fatalf("runWithIO() = %d, want 0; output = %s", got, output.String())
	}
	var response struct {
		Data []struct {
			Method string `json:"method"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode commands response: %v", err)
	}
	if len(response.Data) != 25 || response.Data[0].Method == "daemon.shutdown" {
		t.Fatalf("commands = %+v", response.Data)
	}
	foundRunCancel := false
	for _, command := range response.Data {
		if command.Method == "run.cancel" {
			foundRunCancel = true
			break
		}
	}
	if !foundRunCancel {
		t.Fatalf("commands omit owner run.cancel: %+v", response.Data)
	}
}

func TestAgentCommandUsesRunSocketAndGrant(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "agent.sock")
	requests := make(chan contracts.Request, 1)
	server, err := ipc.Listen(socket, func(_ context.Context, request contracts.Request) (contracts.Response, error) {
		requests <- request
		return contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: request.RequestID, OK: true, Data: json.RawMessage(`{"items":[]}`), Meta: &contracts.Meta{ProfileID: "work"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		<-done
	})
	t.Setenv(access.EnvSocket, socket)
	t.Setenv(access.EnvProfile, "work")
	t.Setenv(access.EnvRun, "run-1")
	t.Setenv(access.EnvGrant, "grant-1")
	t.Setenv(access.EnvMethods, `["message.history"]`)

	var output bytes.Buffer
	params := `{"conversation_id":"conversation-1","limit":1,"idempotency_key":"query-1"}`
	if got := runWithIO([]string{"message", "history", "--params-stdin"}, strings.NewReader(params), &output, testRoots(t)); got != 0 {
		t.Fatalf("agent command exit = %d: %s", got, output.String())
	}
	request := <-requests
	if request.ProfileID != "work" || request.Method != "message.history" || request.Grant != "grant-1" || request.IdempotencyKey != "query-1" {
		t.Fatalf("Agent request = %+v", request)
	}
}

func testRoots(t *testing.T) commandRoots {
	t.Helper()
	root := t.TempDir()
	return commandRoots{configDir: filepath.Join(root, "config"), dataDir: filepath.Join(root, "data"), runtimeDir: filepath.Join(root, "runtime")}
}
