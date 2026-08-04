package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/access"
	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	acpprovider "github.com/abd-im/abd-im-cli/internal/agent/provider/acp"
	codexprovider "github.com/abd-im/abd-im-cli/internal/agent/provider/codex"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	runmanager "github.com/abd-im/abd-im-cli/internal/agent/run"
	"github.com/abd-im/abd-im-cli/internal/bridge"
	blacklistcapability "github.com/abd-im/abd-im-cli/internal/capability/blacklist"
	conversationcapability "github.com/abd-im/abd-im-cli/internal/capability/conversation"
	friendcapability "github.com/abd-im/abd-im-cli/internal/capability/friend"
	groupcapability "github.com/abd-im/abd-im-cli/internal/capability/group"
	messagecapability "github.com/abd-im/abd-im-cli/internal/capability/message"
	"github.com/abd-im/abd-im-cli/internal/cli"
	"github.com/abd-im/abd-im-cli/internal/commands"
	"github.com/abd-im/abd-im-cli/internal/connector"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/daemon"
	"github.com/abd-im/abd-im-cli/internal/events"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/operation"
	"github.com/abd-im/abd-im-cli/internal/profile"
	"github.com/abd-im/abd-im-cli/internal/reply"
	operationsservice "github.com/abd-im/abd-im-cli/internal/service/operations"
	profileservice "github.com/abd-im/abd-im-cli/internal/service/profile"
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
	profileSet := false
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--profile":
			if len(args) < 2 {
				return writeInvalidArgument(output, requestID, "--profile requires a value")
			}
			profileName, args = args[1], args[2:]
			profileSet = true
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

	agentContext, agentMode, err := access.FromEnvironment(os.Getenv)
	if err != nil {
		return writeInvalidArgument(output, requestID, err.Error())
	}
	if agentMode {
		if profileSet && profileName != agentContext.ProfileID {
			return writeInvalidArgument(output, requestID, "--profile cannot override the Agent run profile")
		}
		return runTypedCommand(ctx, args, input, output, format, requestID, agentContext.ProfileID, agentContext.Socket, agentContext.Grant, commands.Run(agentContext.AllowedMethods))
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
	if len(args) >= 1 && args[0] == "inbound" {
		if len(args) < 2 || args[1] != "tools" {
			return writeInvalidArgument(output, requestID, "unsupported inbound command")
		}
		return runInboundTools(ctx, args[2:], output, roots, profileName)
	}
	if len(args) == 1 && args[0] == "__serve" {
		return runDaemonServe(ctx, output, roots, profileName, requestID, format)
	}
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, profileName)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	return runTypedCommand(ctx, args, input, output, format, requestID, profileName, paths.Socket, "", commands.Owner())
}

func runTypedCommand(ctx context.Context, args []string, input io.Reader, output io.Writer, format cli.Output, requestID, profileID, socket, credential string, catalog []commands.Command) int {
	if len(args) == 1 && args[0] == "commands" {
		payload, err := json.Marshal(catalog)
		if err != nil {
			return writeLocalErrorForFormat(output, format, requestID, err)
		}
		response := contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: requestID, OK: true, Data: payload, Meta: &contracts.Meta{ProfileID: profileID}}
		if err := cli.WriteResponse(output, format, response); err != nil {
			return 1
		}
		return 0
	}
	method, consumed := commands.Resolve(args, catalog)
	if method == "" {
		return writeInvalidArgument(output, requestID, "unsupported command")
	}
	params, err := commandParams(args[consumed:], input)
	if err != nil {
		return writeInvalidArgument(output, requestID, err.Error())
	}
	response, err := ipc.Call(ctx, socket, contracts.Request{
		APIVersion:     contracts.APIVersionV1,
		RequestID:      requestID,
		ProfileID:      profileID,
		Method:         method,
		Params:         params,
		Grant:          credential,
		IdempotencyKey: commandIdempotencyKey(params),
	})
	if err != nil {
		response = cli.ErrorResponse(requestID, contracts.CodeDaemonUnavailable, errors.New("daemon is unavailable"))
	}
	if err := cli.WriteResponse(output, format, response); err != nil {
		return 1
	}
	return cli.ExitCode(response)
}

// runDaemonServe composes the fixed daemon path with the profile's allowlisted
// Agent. Each run receives a fresh private abdim CLI context.
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
	launch, err := agentLaunch(item.Agent)
	if err != nil {
		return writeInvalidArgument(output, requestID, err.Error())
	}
	agentExecutable, err := exec.LookPath(launch.command)
	if err != nil {
		return writeInvalidArgument(output, requestID, "configured Agent is unavailable on PATH")
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
	groupCreate, err := groupcapability.New(groupOperations, groupCreator)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	groupMembership, err := groupcapability.NewMembership(groupOperations, groupMembershipActions)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	groupAdministration, err := groupcapability.NewAdministration(groupOperations, groupAdministrationActions)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageSend, err := messagecapability.New(groupOperations, messageSender)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageAt, err := messagecapability.NewAt(groupOperations, messageAtSender)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageQuote, err := messagecapability.NewQuote(groupOperations, messageQuoteSource, messageQuoteSource)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageLocation, err := messagecapability.NewLocation(groupOperations, messageLocationSender)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageCustom, err := messagecapability.NewCustom(groupOperations, messageCustomSender)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageRevoke, err := messagecapability.NewRevoke(groupOperations, messageRevokeSource)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	messageMedia, err := messagecapability.NewMedia(groupOperations, messageAttachments, messageMediaSender)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	conversationMarkRead, err := conversationcapability.New(groupOperations, conversationMarkReadSource, conversationMarkReadSource)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	conversationPinned, err := conversationcapability.NewSetPinned(groupOperations, conversationSettingsSource)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	conversationReceiveOption, err := conversationcapability.NewSetReceiveOption(groupOperations, conversationSettingsSource)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	friendHandler, err := friendcapability.New(groupOperations, friendActions)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	blacklistHandler, err := blacklistcapability.New(groupOperations, blacklistActions)
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
	services, err := daemon.NewOwnerServices(
		item.Name, profileSource, conversationSource, messageSource, groupSource, socialSource,
	)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	agent, err := newAgentProvider(item.Agent, agentExecutable, launch.args, paths.RunsDir)
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
	runs, err := runmanager.NewManager(runmanager.Config{Provider: agent, MaxQueue: 2, Deadline: 2 * time.Minute, Observer: runTracker})
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
		Policy:    directMessagePolicy(item.Deployment.UserID, item.InboundToolsEnabled, methods),
		GrantTTL:  2 * time.Minute,
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

func directMessagePolicy(userID string, toolsEnabled bool, methods []proxy.Method) daemon.Policy {
	methodNames := make([]string, 0, len(methods))
	if toolsEnabled {
		for _, method := range methods {
			methodNames = append(methodNames, method.Name)
		}
	}
	return daemon.PolicyFunc(func(_ context.Context, inbound daemon.InboundContext) (daemon.Decision, bool, error) {
		senderID := strings.TrimSpace(inbound.SenderID)
		if senderID == "" || senderID == userID || inbound.SessionType != 1 {
			return daemon.Decision{}, false, nil
		}
		decision := daemon.Decision{Principal: "openim:" + senderID, RateBudget: 1}
		if toolsEnabled {
			decision.Methods = methodNames
			decision.HistoryBeforeTrigger = true
			decision.AttachmentByteLimit = 32 * 1024 * 1024
			decision.RateBudget = 64
		}
		return decision, true, nil
	})
}

type agentLaunchSpec struct {
	command string
	args    []string
}

func agentLaunch(agent string) (agentLaunchSpec, error) {
	agent, err := profile.NormalizeAgent(agent)
	if err != nil {
		return agentLaunchSpec{}, err
	}
	switch agent {
	case "codex":
		return agentLaunchSpec{command: "codex"}, nil
	case "hermes":
		return agentLaunchSpec{command: "hermes", args: []string{"acp"}}, nil
	case "openclaw":
		return agentLaunchSpec{command: "openclaw", args: []string{"acp"}}, nil
	default:
		return agentLaunchSpec{}, profile.ErrInvalidAgent
	}
}

func agentEnvironment(agent string) []string {
	result := []string{
		"PATH=" + os.Getenv("PATH"),
		"TERM=dumb",
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		result = append(result, "HOME="+home)
	}
	if agent == "codex" {
		result = append(result, "NO_BROWSER=1")
		if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); filepath.IsAbs(home) {
			result = append(result, "CODEX_HOME="+filepath.Clean(home))
		}
	}
	return result
}

func newAgentProvider(agent, executable string, args []string, workingDir string) (contracts.Provider, error) {
	if agent == "codex" {
		codexHome, err := currentCodexHome()
		if err != nil {
			return nil, err
		}
		return codexprovider.New(codexprovider.Config{
			Executable:      executable,
			WorkingDir:      workingDir,
			SourceCodexHome: codexHome,
			Environment:     agentEnvironment(agent),
			CLICommand:      executablePath(),
		})
	}
	return acpprovider.New(acpprovider.Config{
		Executable:  executable,
		Args:        args,
		WorkingDir:  workingDir,
		Environment: agentEnvironment(agent),
		CLICommand:  executablePath(),
	})
}

func currentCodexHome() (string, error) {
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		if !filepath.IsAbs(value) {
			return "", errors.New("CODEX_HOME must be absolute")
		}
		return filepath.Clean(value), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", errors.New("resolve current user Codex home")
	}
	return filepath.Join(home, ".codex"), nil
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

func daemonSDKConfig(paths profile.Paths, deployment profile.Deployment) sdk_struct.IMConfig {
	return sdk_struct.IMConfig{
		SystemType:  runtime.GOOS,
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

func executablePath() string {
	path, err := os.Executable()
	if err != nil || !filepath.IsAbs(path) {
		return ""
	}
	return path
}

func commandParams(args []string, input io.Reader) (json.RawMessage, error) {
	if len(args) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if len(args) != 1 || args[0] != "--params-stdin" {
		return nil, errors.New("command only accepts --params-stdin")
	}
	if input == nil {
		return nil, errors.New("command parameters are required")
	}
	const maxParamsBytes = 1 << 20
	payload, err := io.ReadAll(io.LimitReader(input, maxParamsBytes+1))
	if err != nil {
		return nil, errors.New("read command parameters")
	}
	if len(payload) > maxParamsBytes {
		return nil, errors.New("command parameters are too large")
	}
	payload = []byte(strings.TrimSpace(string(payload)))
	var object map[string]json.RawMessage
	if len(payload) == 0 || json.Unmarshal(payload, &object) != nil || object == nil {
		return nil, errors.New("command parameters must be a JSON object")
	}
	return json.RawMessage(payload), nil
}

func commandIdempotencyKey(params json.RawMessage) string {
	var values map[string]json.RawMessage
	if json.Unmarshal(params, &values) != nil {
		return ""
	}
	var key string
	_ = json.Unmarshal(values["idempotency_key"], &key)
	return key
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
