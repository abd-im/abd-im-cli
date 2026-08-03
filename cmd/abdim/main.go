package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/provider"
	codexprovider "github.com/abd-im/abd-im-cli/internal/agent/provider/codex"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	runmanager "github.com/abd-im/abd-im-cli/internal/agent/run"
	"github.com/abd-im/abd-im-cli/internal/bridge"
	"github.com/abd-im/abd-im-cli/internal/capability"
	blacklistcapability "github.com/abd-im/abd-im-cli/internal/capability/blacklist"
	conversationcapability "github.com/abd-im/abd-im-cli/internal/capability/conversation"
	friendcapability "github.com/abd-im/abd-im-cli/internal/capability/friend"
	groupcapability "github.com/abd-im/abd-im-cli/internal/capability/group"
	messagecapability "github.com/abd-im/abd-im-cli/internal/capability/message"
	"github.com/abd-im/abd-im-cli/internal/cli"
	"github.com/abd-im/abd-im-cli/internal/connector"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/daemon"
	"github.com/abd-im/abd-im-cli/internal/events"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	mcpowner "github.com/abd-im/abd-im-cli/internal/mcp/owner"
	mcpstdio "github.com/abd-im/abd-im-cli/internal/mcp/stdio"
	"github.com/abd-im/abd-im-cli/internal/operation"
	"github.com/abd-im/abd-im-cli/internal/profile"
	"github.com/abd-im/abd-im-cli/internal/reply"
	conversationservice "github.com/abd-im/abd-im-cli/internal/service/conversation"
	groupservice "github.com/abd-im/abd-im-cli/internal/service/group"
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	operationsservice "github.com/abd-im/abd-im-cli/internal/service/operations"
	profileservice "github.com/abd-im/abd-im-cli/internal/service/profile"
	socialservice "github.com/abd-im/abd-im-cli/internal/service/social"
	"github.com/abd-im/abd-im-sdk-core/v3/open_im_sdk"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return writeLocalError(os.Stdout, "cli", fmt.Errorf("resolve config directory: %w", err))
	}
	dataDir, err := os.UserCacheDir()
	if err != nil {
		return writeLocalError(os.Stdout, "cli", fmt.Errorf("resolve data directory: %w", err))
	}
	return runWithIOStreams(args, os.Stdin, os.Stdout, os.Stderr, commandRoots{configDir: configDir, dataDir: dataDir, runtimeDir: os.TempDir()})
}

type commandRoots struct {
	configDir  string
	dataDir    string
	runtimeDir string
}

func runWithIO(args []string, input io.Reader, output io.Writer, roots commandRoots) int {
	return runWithIOStreams(args, input, output, io.Discard, roots)
}

func runWithIOStreams(args []string, input io.Reader, output, prompt io.Writer, roots commandRoots) int {
	profileName := "default"
	requestID := "cli"
	format := cli.OutputJSON
	var timeout time.Duration
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--profile":
			if len(args) < 2 {
				return writeInvalidArgument(output, requestID, "--profile requires a value")
			}
			profileName, args = args[1], args[2:]
		case "--request-id":
			if len(args) < 2 {
				return writeInvalidArgument(output, requestID, "--request-id requires a value")
			}
			requestID, args = args[1], args[2:]
		case "--output":
			if len(args) < 2 {
				return writeInvalidArgument(output, requestID, "--output requires a value")
			}
			format, args = cli.Output(args[1]), args[2:]
			if format != cli.OutputJSON && format != cli.OutputJSONL {
				return writeInvalidArgument(output, requestID, "--output must be json or jsonl")
			}
		case "--timeout":
			if len(args) < 2 {
				return writeInvalidArgument(output, requestID, "--timeout requires a value")
			}
			value, err := time.ParseDuration(args[1])
			if err != nil || value <= 0 {
				return writeInvalidArgument(output, requestID, "--timeout must be a positive duration")
			}
			timeout, args = value, args[2:]
		default:
			return writeInvalidArgument(output, requestID, "unsupported global flag")
		}
	}
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if len(args) >= 1 && args[0] == "setup" {
		return runSetup(ctx, args[1:], input, output, prompt, roots, profileName)
	}
	if len(args) >= 1 && args[0] == "start" {
		return runStart(ctx, args[1:], output, roots, profileName)
	}
	if len(args) >= 1 && args[0] == "stop" {
		return runStop(ctx, args[1:], output, roots, profileName)
	}
	if len(args) >= 1 && args[0] == "restart" {
		return runRestart(ctx, args[1:], output, roots, profileName)
	}
	if len(args) >= 1 && args[0] == "status" {
		return runStatus(ctx, args[1:], output, roots, profileName)
	}
	if len(args) == 1 && args[0] == "__serve" {
		return runDaemonServe(ctx, output, roots, profileName, requestID, format)
	}
	if len(args) == 2 && args[0] == "mcp" && args[1] == "serve" {
		return runOwnerMCP(ctx, input, output, roots, profileName, requestID)
	}
	if len(args) >= 3 && args[0] == "mcp" && args[1] == "provider" && args[2] == "bridge" {
		return runProviderMCPBridge(args[3:], input, output)
	}
	method, consumed := ownerMethod(args)
	if method == "" {
		return writeInvalidArgument(output, requestID, "unsupported owner command")
	}
	params, err := ownerParams(args[consumed:], input)
	if err != nil {
		return writeInvalidArgument(output, requestID, err.Error())
	}
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, profileName)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	response, err := ipc.Call(ctx, paths.Socket, contracts.Request{
		APIVersion: contracts.APIVersionV1,
		RequestID:  requestID,
		ProfileID:  profileName,
		Method:     method,
		Params:     params,
	})
	if err != nil {
		response = cli.ErrorResponse(requestID, contracts.CodeDaemonUnavailable, errors.New("daemon is unavailable"))
	}
	if err := cli.WriteResponse(output, format, response); err != nil {
		return 1
	}
	return cli.ExitCode(response)
}

// runDaemonServe composes the fixed daemon path with the current user's local
// Codex CLI. Each run receives a fresh private MCP configuration.
func runDaemonServe(ctx context.Context, output io.Writer, roots commandRoots, profileName, requestID string, format cli.Output) int {
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, profileName)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	if err := paths.EnsurePrivate(); err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	item, err := profile.Load(paths.ConfigFile)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	if err := item.Deployment.Validate(); err != nil {
		return writeInvalidArgument(output, requestID, "profile deployment is not configured")
	}
	codexExecutable, err := exec.LookPath("codex")
	if err != nil {
		return writeInvalidArgument(output, requestID, "Codex executable is unavailable on PATH")
	}
	sourceCodexHome, err := currentCodexHome()
	if err != nil {
		return writeInvalidArgument(output, requestID, "current user Codex login is unavailable")
	}

	credentials, err := profile.NewFileStore(roots.dataDir)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	prepared, err := connector.Prepare(ctx, connector.Config{
		ProfileID:     item.Name,
		UserID:        item.Deployment.UserID,
		CredentialRef: item.CredentialRef,
		Credentials:   credentials,
		SDKConfig:     daemonSDKConfig(paths, item.Deployment),
	})
	if err != nil {
		return writeDaemonServeFailure(output, format, requestID)
	}

	store, err := control.Open(paths.ControlDB)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	defer store.Close()
	if err := store.PutProfile(ctx, control.Profile{ID: item.Name}); err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	ledger, err := events.NewLedger(store)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	groupSource, err := prepared.GroupSource()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	conversationSource, err := prepared.ConversationSource()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageSource, err := prepared.MessageSource()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	socialSource, err := prepared.SocialSource()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	groupCreator, err := prepared.GroupCreator()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	groupMembershipActions, err := prepared.GroupMembershipActions()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	groupAdministrationActions, err := prepared.GroupAdministrationActions()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageSender, err := prepared.MessageSender()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageAtSender, err := prepared.MessageAtSender()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageLocationSender, err := prepared.MessageLocationSender()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageCustomSender, err := prepared.MessageCustomSender()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageMediaSender, err := prepared.MessageMediaSender()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageQuoteSource, err := prepared.MessageQuoteSource()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	conversationMarkReadSource, err := prepared.ConversationMarkRead()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	conversationSettingsSource, err := prepared.ConversationSettings()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	friendActions, err := prepared.FriendActions()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	blacklistActions, err := prepared.BlacklistActions()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageRevokeSource, err := prepared.MessageRevokeSource()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageAttachments, err := messagecapability.NewAttachmentStore(store, paths)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	groupOperations, err := operation.NewGuard(store)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	evidenceGate, err := capability.NewEvidenceGate([]capability.Compatibility{capability.SingleCodexOpenIMCompatibility})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	runtimeCompatibility := capability.Compatibility{
		Provider:    "codex",
		MCPProtocol: mcpstdio.ProtocolVersion,
		SDKVersion:  open_im_sdk.GetSdkVersion(),
		ServerAPI:   capability.SingleCodexOpenIMCompatibility.ServerAPI,
	}
	newManifest := func(entries []capability.Entry) (*capability.Manifest, error) {
		return evidenceGate.Manifest(runtimeCompatibility, entries)
	}
	groupManifest, err := newManifest([]capability.Entry{
		{Method: groupcapability.Method, Scope: groupcapability.Scope, Status: capability.Available},
		{Method: groupcapability.JoinMethod, Scope: groupcapability.JoinScope, Status: capability.Available},
		{Method: groupcapability.LeaveMethod, Scope: groupcapability.LeaveScope, Status: capability.Available},
		{Method: groupcapability.InviteMembersMethod, Scope: groupcapability.InviteMembersScope, Status: capability.Available},
		{Method: groupcapability.RemoveMembersMethod, Scope: groupcapability.RemoveMembersScope, Status: capability.Available},
		{Method: groupcapability.SetInfoMethod, Scope: groupcapability.SetInfoScope, Status: capability.Available},
		{Method: groupcapability.SetMuteMethod, Scope: groupcapability.SetMuteScope, Status: capability.Available},
		{Method: groupcapability.SetMemberMuteMethod, Scope: groupcapability.SetMemberMuteScope, Status: capability.Available},
		{Method: groupcapability.TransferOwnerMethod, Scope: groupcapability.TransferOwnerScope, Status: capability.Available},
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	groupCreate, err := groupcapability.New(groupManifest, groupOperations, groupCreator)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	groupMembership, err := groupcapability.NewMembership(groupManifest, groupOperations, groupMembershipActions)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	groupAdministration, err := groupcapability.NewAdministration(groupManifest, groupOperations, groupAdministrationActions)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageManifest, err := newManifest([]capability.Entry{
		{Method: messagecapability.Method, Scope: messagecapability.Scope, Status: capability.Available},
		{Method: messagecapability.AtMethod, Scope: messagecapability.AtScope, Status: capability.Available},
		{Method: messagecapability.QuoteMethod, Scope: messagecapability.QuoteScope, Status: capability.Available},
		{Method: messagecapability.LocationMethod, Scope: messagecapability.LocationScope, Status: capability.Available},
		{Method: messagecapability.CustomMethod, Scope: messagecapability.CustomScope, Status: capability.Available},
		{Method: messagecapability.RevokeMethod, Scope: messagecapability.RevokeScope, Status: capability.Available},
		{Method: messagecapability.ImageMethod, Scope: messagecapability.ImageScope, Status: capability.Available},
		{Method: messagecapability.FileMethod, Scope: messagecapability.FileScope, Status: capability.Available},
		{Method: messagecapability.SoundMethod, Scope: messagecapability.SoundScope, Status: capability.Available},
		{Method: messagecapability.VideoMethod, Scope: messagecapability.VideoScope, Status: capability.Available},
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageSend, err := messagecapability.New(messageManifest, groupOperations, messageSender)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageAt, err := messagecapability.NewAt(messageManifest, groupOperations, messageAtSender)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageQuote, err := messagecapability.NewQuote(messageManifest, groupOperations, messageQuoteSource, messageQuoteSource)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageLocation, err := messagecapability.NewLocation(messageManifest, groupOperations, messageLocationSender)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageCustom, err := messagecapability.NewCustom(messageManifest, groupOperations, messageCustomSender)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageRevoke, err := messagecapability.NewRevoke(messageManifest, groupOperations, messageRevokeSource)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageMedia, err := messagecapability.NewMedia(messageManifest, groupOperations, messageAttachments, messageMediaSender)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	conversationManifest, err := newManifest([]capability.Entry{
		{Method: conversationcapability.Method, Scope: conversationcapability.Scope, Status: capability.Available},
		{Method: conversationcapability.SetPinnedMethod, Scope: conversationcapability.SetPinnedScope, Status: capability.Available},
		{Method: conversationcapability.SetReceiveOptionMethod, Scope: conversationcapability.SetReceiveOptionScope, Status: capability.Available},
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	conversationMarkRead, err := conversationcapability.New(conversationManifest, groupOperations, conversationMarkReadSource, conversationMarkReadSource)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	conversationPinned, err := conversationcapability.NewSetPinned(conversationManifest, groupOperations, conversationSettingsSource)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	conversationReceiveOption, err := conversationcapability.NewSetReceiveOption(conversationManifest, groupOperations, conversationSettingsSource)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	friendManifest, err := newManifest([]capability.Entry{
		{Method: friendcapability.RequestMethod, Scope: friendcapability.RequestScope, Status: capability.Available},
		{Method: friendcapability.RespondMethod, Scope: friendcapability.RespondScope, Status: capability.Available},
		{Method: friendcapability.DeleteMethod, Scope: friendcapability.DeleteScope, Status: capability.Available},
		{Method: friendcapability.SetRemarkMethod, Scope: friendcapability.SetRemarkScope, Status: capability.Available},
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	friendHandler, err := friendcapability.New(friendManifest, groupOperations, friendActions)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	blacklistManifest, err := newManifest([]capability.Entry{
		{Method: blacklistcapability.AddMethod, Scope: blacklistcapability.AddScope, Status: capability.Available},
		{Method: blacklistcapability.RemoveMethod, Scope: blacklistcapability.RemoveScope, Status: capability.Available},
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	blacklistHandler, err := blacklistcapability.New(blacklistManifest, groupOperations, blacklistActions)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	var runtime *daemon.Runtime
	profileSource, err := prepared.ProfileSource(func() profileservice.DaemonStatus {
		state := bridge.StateNew
		if runtime != nil {
			state = runtime.State()
		}
		return profileservice.DaemonStatus{
			ProfileID:        item.Name,
			State:            string(state),
			PID:              os.Getpid(),
			SDKVersion:       open_im_sdk.GetSdkVersion(),
			CredentialsValid: state == bridge.StateReady,
		}
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	services, err := daemon.NewOwnerServicesWithVerifiedProfileConversationMessageGroupAndSocial(
		item.Name,
		profileSource, profileservice.VerifiedCapabilities(open_im_sdk.GetSdkVersion()),
		conversationSource, conversationservice.VerifiedCapabilities(open_im_sdk.GetSdkVersion()),
		messageSource, messageservice.VerifiedCapabilities(open_im_sdk.GetSdkVersion()),
		groupSource, groupservice.VerifiedCapabilities(open_im_sdk.GetSdkVersion()),
		socialSource, socialservice.VerifiedCapabilities(open_im_sdk.GetSdkVersion()),
	)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	codex, err := codexprovider.New(codexprovider.Config{
		Executable:      codexExecutable,
		WorkingDir:      paths.RunsDir,
		SourceCodexHome: sourceCodexHome,
		Environment:     codexEnvironment(),
		BridgeCommand:   executablePath(),
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	singleProvider, err := provider.New(codex)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	runTracker, err := operationsservice.NewTracker(store)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	if err := runTracker.Recover(ctx, item.Name); err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	runs, err := runmanager.NewManager(runmanager.Config{Provider: singleProvider, MaxQueue: 2, Deadline: 2 * time.Minute, Observer: runTracker})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	runOperations, err := operationsservice.New(item.Name, store, runs)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	ownerMethods, err := daemon.OwnerMethods(services)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	runOwnerMethods, err := daemon.RunOperationOwnerMethods(runOperations)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	dispatcher, err := daemon.NewDispatcher(item.Name, append(ownerMethods, runOwnerMethods...))
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	replies, err := reply.New(store, prepared.Adapter)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	methods := append(serviceMethods(services), groupCreate.ProxyMethod(), messageSend.ProxyMethod(), messageAt.ProxyMethod(), messageQuote.ProxyMethod(), messageLocation.ProxyMethod(), messageCustom.ProxyMethod(), messageRevoke.ProxyMethod())
	methods = append(methods, groupMembership.ProxyMethods()...)
	methods = append(methods, groupAdministration.ProxyMethods()...)
	methods = append(methods, messageMedia.ProxyMethods()...)
	methods = append(methods, conversationMarkRead.ProxyMethod(), conversationPinned.ProxyMethod(), conversationReceiveOption.ProxyMethod())
	methods = append(methods, friendHandler.ProxyMethods()...)
	methods = append(methods, blacklistHandler.ProxyMethods()...)
	inbound, err := daemon.New(daemon.Config{
		ProfileID: item.Name,
		Ledger:    ledger,
		Replies:   replies,
		Runs:      runs,
		Grants:    grant.NewStore(),
		Methods:   methods,
		Policy:    fullInboundPolicy(methods),
		GrantTTL:  2 * time.Minute,
		Accept:    acceptAllInbound(item.Deployment.UserID),
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	runtime, err = daemon.NewRuntime(daemon.RuntimeConfig{
		SDKFactory: prepared.SDKFactory(),
		LockFile:   paths.LockFile,
		SocketPath: paths.Socket,
		Inbound:    inbound,
		Handler:    dispatcher.Handle,
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	serveContext, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runtime.Start(serveContext); err != nil {
		return writeDaemonServeFailure(output, format, requestID)
	}
	payload, err := json.Marshal(struct {
		ProfileID string `json:"profile_id"`
		Serving   bool   `json:"serving"`
	}{ProfileID: item.Name, Serving: true})
	if err != nil {
		_ = runtime.Shutdown(context.Background())
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	response := contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: requestID, OK: true, Data: payload, Meta: &contracts.Meta{ProfileID: item.Name}}
	if err := cli.WriteResponse(output, format, response); err != nil {
		_ = runtime.Shutdown(context.Background())
		return 1
	}
	if err := runtime.Wait(serveContext); err != nil && !errors.Is(err, context.Canceled) {
		return 1
	}
	return 0
}

func fullInboundPolicy(methods []proxy.Method) daemon.Policy {
	methodNames := make([]string, 0, len(methods))
	for _, method := range methods {
		methodNames = append(methodNames, method.Name)
	}
	return daemon.PolicyFunc(func(context.Context, contracts.Event) (daemon.Decision, bool, error) {
		return daemon.Decision{
			Principal:           "inbound",
			Methods:             methodNames,
			FullAccess:          true,
			AttachmentByteLimit: 32 * 1024 * 1024,
			RateBudget:          64,
		}, true, nil
	})
}

func currentCodexHome() (string, error) {
	home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(userHome, ".codex")
	}
	if !filepath.IsAbs(home) {
		return "", errors.New("CODEX_HOME must be absolute")
	}
	info, err := os.Stat(filepath.Join(home, "auth.json"))
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("Codex auth.json is unavailable")
	}
	return filepath.Clean(home), nil
}

func codexEnvironment() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"TERM=dumb",
	}
}

func serviceMethods(services daemon.OwnerServices) []proxy.Method {
	methods := make([]proxy.Method, 0, 22)
	methods = append(methods, services.Profile.Methods()...)
	methods = append(methods, services.Conversation.Methods()...)
	methods = append(methods, services.Message.Methods()...)
	methods = append(methods, services.Group.Methods()...)
	methods = append(methods, services.Social.Methods()...)
	return methods
}

func acceptAllInbound(userID string) func(contracts.SDKEvent) bool {
	return func(event contracts.SDKEvent) bool {
		if event.Type != string(contracts.EventMessageReceived) {
			return false
		}
		reference := struct {
			SenderID    string `json:"sender_id"`
			GroupID     string `json:"group_id"`
			SessionType int32  `json:"session_type"`
		}{}
		if json.Unmarshal(event.Data, &reference) != nil || strings.TrimSpace(reference.SenderID) == "" || reference.SenderID == userID {
			return false
		}
		switch reference.SessionType {
		case 1:
			return true
		case 2, 3:
			return strings.TrimSpace(reference.GroupID) != ""
		default:
			return false
		}
	}
}

func daemonSDKConfig(paths profile.Paths, deployment profile.Deployment) sdk_struct.IMConfig {
	return sdk_struct.IMConfig{
		SystemType:  "linux",
		PlatformID:  deployment.PlatformID,
		ApiAddr:     deployment.APIAddr,
		WsAddr:      deployment.WSAddr,
		DataDir:     paths.SDKDir,
		LogLevel:    4,
		LogFilePath: filepath.Join(paths.LogsDir, "sdk.log"),
	}
}

func writeDaemonServeFailure(output io.Writer, format cli.Output, requestID string) int {
	response := cli.ErrorResponse(requestID, contracts.CodeConnectionUnavailable, errors.New("daemon failed to start"))
	if err := cli.WriteResponse(output, format, response); err != nil {
		return 1
	}
	return cli.ExitCode(response)
}

func runOwnerMCP(ctx context.Context, input io.Reader, output io.Writer, roots commandRoots, profileName, requestID string) int {
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, profileName)
	if err != nil {
		return writeLocalError(output, requestID, err)
	}
	server, err := mcpowner.New(profileName, func(callCtx context.Context, request contracts.Request) (contracts.Response, error) {
		return ipc.Call(callCtx, paths.Socket, request)
	}, mcpowner.DefaultTools())
	if err != nil {
		return writeLocalError(output, requestID, err)
	}
	if err := server.Serve(ctx, input, output); err != nil && !errors.Is(err, context.Canceled) {
		return 1
	}
	return 0
}

// runProviderMCPBridge is only launched from the run-private Codex config.
// It transports raw MCP JSON-RPC and deliberately has no profile, owner RPC,
// SDK, or arbitrary-command arguments.
func runProviderMCPBridge(args []string, input io.Reader, output io.Writer) int {
	if len(args) != 2 || args[0] != "--socket" || !filepath.IsAbs(args[1]) {
		return 2
	}
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: args[1], Net: "unix"})
	if err != nil {
		return 1
	}
	defer connection.Close()
	inputDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(connection, input)
		_ = connection.CloseWrite()
		close(inputDone)
	}()
	_, err = io.Copy(output, connection)
	<-inputDone
	if err != nil {
		return 1
	}
	return 0
}

func executablePath() string {
	path, err := os.Executable()
	if err != nil || !filepath.IsAbs(path) {
		return ""
	}
	return path
}

func ownerMethod(args []string) (string, int) {
	methods := make(map[string]struct{}, len(mcpowner.DefaultTools()))
	for _, tool := range mcpowner.DefaultTools() {
		methods[tool.Method] = struct{}{}
	}
	for index, argument := range args {
		if strings.HasPrefix(argument, "--") {
			break
		}
		method := strings.Join(args[:index+1], ".")
		if _, exists := methods[method]; exists {
			return method, index + 1
		}
	}
	return "", 0
}

func ownerParams(args []string, input io.Reader) (json.RawMessage, error) {
	if len(args) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if len(args) != 1 || args[0] != "--params-stdin" {
		return nil, errors.New("owner query only accepts --params-stdin")
	}
	if input == nil {
		return nil, errors.New("owner query parameters are required")
	}
	const maxParamsBytes = 1 << 20
	payload, err := io.ReadAll(io.LimitReader(input, maxParamsBytes+1))
	if err != nil {
		return nil, errors.New("read owner query parameters")
	}
	if len(payload) > maxParamsBytes {
		return nil, errors.New("owner query parameters are too large")
	}
	payload = []byte(strings.TrimSpace(string(payload)))
	var object map[string]json.RawMessage
	if len(payload) == 0 || json.Unmarshal(payload, &object) != nil || object == nil {
		return nil, errors.New("owner query parameters must be a JSON object")
	}
	return json.RawMessage(payload), nil
}

func writeInvalidArgument(output io.Writer, requestID, message string) int {
	response := cli.ErrorResponse(requestID, contracts.CodeInvalidArgument, errors.New(message))
	_ = cli.WriteResponse(output, cli.OutputJSON, response)
	return cli.ExitCode(response)
}

func writeLocalError(output io.Writer, requestID string, err error) int {
	return writeLocalErrorForFormat(output, cli.OutputJSON, requestID, err)
}

func writeLocalErrorForFormat(output io.Writer, format cli.Output, requestID string, err error) int {
	code := contracts.CodeInternal
	if cli.IsInvalidArgument(err) {
		code = contracts.CodeInvalidArgument
	}
	response := cli.ErrorResponse(requestID, code, err)
	_ = cli.WriteResponse(output, format, response)
	return cli.ExitCode(response)
}
