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

	acpprovider "github.com/abd-im/abd-im-cli/internal/agent/provider/acp"
	codexprovider "github.com/abd-im/abd-im-cli/internal/agent/provider/codex"
	runmanager "github.com/abd-im/abd-im-cli/internal/agent/run"
	"github.com/abd-im/abd-im-cli/internal/bridge"
	"github.com/abd-im/abd-im-cli/internal/cli"
	"github.com/abd-im/abd-im-cli/internal/commands"
	"github.com/abd-im/abd-im-cli/internal/connector"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/daemon"
	"github.com/abd-im/abd-im-cli/internal/events"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/profile"
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	profileservice "github.com/abd-im/abd-im-cli/internal/service/profile"
	"github.com/abd-im/abd-im-cli/skills"
	"github.com/abd-im/abd-im-sdk-core/v3/open_im_sdk"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

func main() { os.Exit(run(os.Args[1:])) }

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
	profileName := strings.TrimSpace(os.Getenv("ABDIM_PROFILE"))
	if profileName == "" {
		profileName = "default"
	}
	requestID := "cli"
	format := cli.OutputJSON
	identity := profile.IdentityBot
	var timeout time.Duration
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--profile":
			if len(args) < 2 {
				return writeInvalidArgument(output, requestID, "--profile requires a value")
			}
			profileName, args = args[1], args[2:]
		case "--as":
			if len(args) < 2 {
				return writeInvalidArgument(output, requestID, "--as requires user or bot")
			}
			var err error
			identity, err = profile.ParseIdentity(args[1])
			if err != nil {
				return writeInvalidArgument(output, requestID, err.Error())
			}
			args = args[2:]
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

	if len(args) > 0 && args[0] == "setup" {
		return runSetup(ctx, args[1:], input, output, prompt, roots, profileName)
	}
	if len(args) > 0 && args[0] == "daemon" {
		if len(args) < 2 {
			return writeInvalidArgument(output, requestID, "daemon requires start, stop, restart, or status")
		}
		switch args[1] {
		case "start":
			return runStart(ctx, args[2:], output, roots, profileName)
		case "stop":
			return runStop(ctx, args[2:], output, roots, profileName)
		case "restart":
			return runRestart(ctx, args[2:], output, roots, profileName)
		case "status":
			return runStatus(ctx, args[2:], output, roots, profileName)
		default:
			return writeInvalidArgument(output, requestID, "unsupported daemon command")
		}
	}
	if len(args) == 1 && args[0] == "__serve" {
		return runDaemonServe(ctx, output, roots, profileName, requestID, format)
	}
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, profileName)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	return runTypedCommand(ctx, args, input, output, format, requestID, profileName, paths.Socket, identity, commands.All())
}

func runTypedCommand(ctx context.Context, args []string, input io.Reader, output io.Writer, format cli.Output, requestID, profileID, socket string, identity profile.Identity, catalog []string) int {
	method, consumed := commands.Resolve(args, catalog)
	if method == "" {
		return writeInvalidArgument(output, requestID, "unsupported command")
	}
	params, err := commandParams(args[consumed:], input)
	if err != nil {
		return writeInvalidArgument(output, requestID, err.Error())
	}
	response, err := ipc.Call(ctx, socket, contracts.Request{
		APIVersion: contracts.APIVersionV1, RequestID: requestID, ProfileID: profileID,
		As: string(identity), Method: method, Params: params,
	})
	if err != nil {
		response = cli.ErrorResponse(requestID, contracts.CodeDaemonUnavailable, errors.New("daemon is unavailable"))
	}
	if err := cli.WriteResponse(output, format, response); err != nil {
		return 1
	}
	return cli.ExitCode(response)
}

func runDaemonServe(ctx context.Context, output io.Writer, roots commandRoots, profileName, requestID string, format cli.Output) int {
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, profileName)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	if err := paths.EnsurePrivate(); err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	item, err := profile.Load(paths.ConfigFile)
	if err != nil || item.Deployment.Validate() != nil {
		return writeInvalidArgument(output, requestID, "profile is not configured; run abdim setup")
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
	user, err := prepareIdentity(ctx, item, paths, profile.IdentityUser, credentials)
	if err != nil {
		return writeDaemonServeFailure(output, format, requestID)
	}
	bot, err := prepareIdentity(ctx, item, paths, profile.IdentityBot, credentials)
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
	agent, err := newAgentProvider(item.Agent, item.Name, agentExecutable, launch.args, paths.RunsDir, paths.DataDir)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	runs, err := runmanager.NewManager(runmanager.Config{
		Provider: agent, Sessions: store, SessionNamespace: item.Agent,
		MaxQueue: 2, MaxConcurrentRuns: 2, Deadline: 2 * time.Minute,
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}

	var runtimeInstance *daemon.Runtime
	status := func() profileservice.DaemonStatus {
		state := bridge.StateNew
		if runtimeInstance != nil {
			state = runtimeInstance.State()
		}
		return profileservice.DaemonStatus{
			ProfileID: item.Name, State: string(state), PID: os.Getpid(),
			SDKVersion: open_im_sdk.GetSdkVersion(), CredentialsValid: state == bridge.StateReady,
		}
	}
	userMethods, userMessages, err := identityMethods(item.Name, user, status)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	botMethods, _, err := identityMethods(item.Name, bot, status)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	workspaceClassifier, err := bot.GroupSource()
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	dispatcher, err := daemon.NewDispatcher(item.Name, userMethods, botMethods)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	inbound, err := daemon.New(daemon.Config{
		ProfileID: item.Name, UserID: item.User.UserID, BotID: item.Bot.UserID,
		Ledger: ledger, Runs: runs, UserMessages: userMessages,
		UserSender: user.Adapter, BotSender: bot.Adapter, WorkspaceSender: bot.Adapter, WorkspaceClassifier: workspaceClassifier,
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	runtimeInstance, err = daemon.NewRuntime(daemon.RuntimeConfig{
		UserSDKFactory: user.SDKFactory(), BotSDKFactory: bot.SDKFactory(),
		LockFile: paths.LockFile, SocketPath: paths.Socket, Inbound: inbound, Handler: dispatcher.Handle,
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	serveContext, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runtimeInstance.Start(serveContext); err != nil {
		return writeDaemonServeFailure(output, format, requestID)
	}
	payload, _ := json.Marshal(struct {
		ProfileID string `json:"profile_id"`
		Serving   bool   `json:"serving"`
	}{ProfileID: item.Name, Serving: true})
	response := contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: requestID, OK: true, Data: payload, Meta: &contracts.Meta{ProfileID: item.Name}}
	if err := cli.WriteResponse(output, format, response); err != nil {
		_ = runtimeInstance.Shutdown(context.Background())
		return 1
	}
	if err := runtimeInstance.Wait(serveContext); err != nil && !errors.Is(err, context.Canceled) {
		return 1
	}
	return 0
}

func prepareIdentity(ctx context.Context, item profile.Profile, paths profile.Paths, identity profile.Identity, credentials profile.CredentialStore) (*connector.Prepared, error) {
	account := item.Bot
	if identity == profile.IdentityUser {
		account = item.User
	}
	return connector.Prepare(ctx, connector.Config{
		ProfileID: item.Name, UserID: account.UserID, CredentialRef: account.CredentialRef,
		Credentials: credentials, SDKConfig: daemonSDKConfig(paths, item.Deployment, identity),
	})
}

func identityMethods(profileID string, prepared *connector.Prepared, status func() profileservice.DaemonStatus) ([]daemon.Method, messageservice.Source, error) {
	profileSource, err := prepared.ProfileSource(status)
	if err != nil {
		return nil, nil, err
	}
	conversationSource, err := prepared.ConversationSource()
	if err != nil {
		return nil, nil, err
	}
	messageSource, err := prepared.MessageSource()
	if err != nil {
		return nil, nil, err
	}
	groupSource, err := prepared.GroupSource()
	if err != nil {
		return nil, nil, err
	}
	socialSource, err := prepared.SocialSource()
	if err != nil {
		return nil, nil, err
	}
	services, err := daemon.NewServices(profileID, profileSource, conversationSource, messageSource, groupSource, socialSource)
	if err != nil {
		return nil, nil, err
	}
	methods, err := daemon.Methods(services)
	if err != nil {
		return nil, nil, err
	}
	send, err := daemon.MessageSendMethod(profileID, prepared.Adapter)
	if err != nil {
		return nil, nil, err
	}
	return append(methods, send), messageSource, nil
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

func agentEnvironment(agent, profileID string) []string {
	path := os.Getenv("PATH")
	if executable := executablePath(); executable != "" {
		path = filepath.Dir(executable) + string(os.PathListSeparator) + path
	}
	result := []string{"PATH=" + path, "TERM=dumb", "ABDIM_PROFILE=" + profileID}
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

func newAgentProvider(agent, profileID, executable string, args []string, workingDir, dataDir string) (contracts.Provider, error) {
	if agent == "codex" {
		codexHome, err := currentCodexHome()
		if err != nil {
			return nil, err
		}
		return codexprovider.New(codexprovider.Config{
			Executable: executable, WorkingDir: workingDir, StateDir: filepath.Join(dataDir, "codex"),
			SourceCodexHome: codexHome, Environment: agentEnvironment(agent, profileID), CLICommand: executablePath(),
		})
	}
	if agent == "hermes" {
		home, err := os.UserHomeDir()
		if err != nil || !filepath.IsAbs(home) {
			return nil, errors.New("resolve Hermes home directory")
		}
		if err := skills.InstallABDHermes(filepath.Join(home, ".hermes")); err != nil {
			return nil, fmt.Errorf("install Hermes ABD IM skill: %w", err)
		}
	}
	return acpprovider.New(acpprovider.Config{
		Executable: executable, Args: args, WorkingDir: workingDir,
		Environment: agentEnvironment(agent, profileID), CLICommand: executablePath(),
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

func daemonSDKConfig(paths profile.Paths, deployment profile.Deployment, identity profile.Identity) sdk_struct.IMConfig {
	dataDir, logName := paths.BotSDKDir, "sdk-bot.log"
	if identity == profile.IdentityUser {
		dataDir, logName = paths.UserSDKDir, "sdk-user.log"
	}
	return sdk_struct.IMConfig{
		SystemType: runtime.GOOS, PlatformID: deployment.PlatformID,
		ApiAddr: deployment.APIAddr, WsAddr: deployment.WSAddr, DataDir: dataDir,
		LogLevel: 4, LogFilePath: filepath.Join(paths.LogsDir, logName),
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
	if err != nil || len(payload) > maxParamsBytes {
		return nil, errors.New("read command parameters")
	}
	payload = []byte(strings.TrimSpace(string(payload)))
	var object map[string]json.RawMessage
	if len(payload) == 0 || json.Unmarshal(payload, &object) != nil || object == nil {
		return nil, errors.New("command parameters must be a JSON object")
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
