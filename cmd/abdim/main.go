package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/provider"
	codexprovider "github.com/abd-im/abd-im-cli/internal/agent/provider/codex"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	runmanager "github.com/abd-im/abd-im-cli/internal/agent/run"
	"github.com/abd-im/abd-im-cli/internal/bridge"
	"github.com/abd-im/abd-im-cli/internal/cli"
	"github.com/abd-im/abd-im-cli/internal/connector"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/control"
	"github.com/abd-im/abd-im-cli/internal/daemon"
	"github.com/abd-im/abd-im-cli/internal/events"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	mcpowner "github.com/abd-im/abd-im-cli/internal/mcp/owner"
	"github.com/abd-im/abd-im-cli/internal/profile"
	"github.com/abd-im/abd-im-cli/internal/reply"
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
	return runWithIO(args, os.Stdin, os.Stdout, commandRoots{configDir: configDir, dataDir: dataDir, runtimeDir: os.TempDir()})
}

type commandRoots struct {
	configDir  string
	dataDir    string
	runtimeDir string
}

func runWithIO(args []string, input io.Reader, output io.Writer, roots commandRoots) int {
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

	if len(args) >= 2 && args[0] == "auth" && args[1] == "import" {
		return runAuthImport(ctx, args[2:], input, output, roots, profileName, requestID, format)
	}
	if len(args) >= 2 && args[0] == "profile" && args[1] == "configure" {
		return runProfileConfigure(args[2:], output, roots, profileName, requestID, format)
	}
	if len(args) >= 2 && args[0] == "daemon" && args[1] == "verify" {
		return runDaemonVerify(ctx, args[2:], output, roots, profileName, requestID, format)
	}
	if len(args) >= 2 && args[0] == "daemon" && args[1] == "serve" {
		return runDaemonServe(ctx, args[2:], output, roots, profileName, requestID, format)
	}
	if len(args) == 2 && args[0] == "mcp" && args[1] == "serve" {
		return runOwnerMCP(ctx, input, output, roots, profileName, requestID)
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

func runAuthImport(ctx context.Context, args []string, input io.Reader, output io.Writer, roots commandRoots, profileName, requestID string, format cli.Output) int {
	tokenFromStdin := false
	allowPlaintext := false
	for _, argument := range args {
		switch argument {
		case "--token-stdin":
			tokenFromStdin = true
		case "--allow-plaintext-credentials":
			allowPlaintext = true
		default:
			return writeInvalidArgument(output, requestID, "auth import accepts the token only from stdin")
		}
	}
	if !tokenFromStdin {
		return writeInvalidArgument(output, requestID, "auth import requires --token-stdin")
	}

	response, err := cli.ImportToken(ctx, input, cli.AuthImportOptions{
		ProfileName:    profileName,
		ConfigDir:      roots.configDir,
		DataDir:        roots.dataDir,
		RuntimeDir:     roots.runtimeDir,
		AllowPlaintext: allowPlaintext,
		RequestID:      requestID,
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	if err := cli.WriteResponse(output, format, response); err != nil {
		return 1
	}
	return cli.ExitCode(response)
}

func runProfileConfigure(args []string, output io.Writer, roots commandRoots, profileName, requestID string, format cli.Output) int {
	var deployment profile.Deployment
	for len(args) > 0 {
		if len(args) < 2 {
			return writeInvalidArgument(output, requestID, "profile configure flags require values")
		}
		flag, value := args[0], args[1]
		switch flag {
		case "--user-id":
			deployment.UserID = value
		case "--api-addr":
			deployment.APIAddr = value
		case "--ws-addr":
			deployment.WSAddr = value
		case "--platform-id":
			parsed, err := strconv.ParseInt(value, 10, 32)
			if err != nil || parsed <= 0 {
				return writeInvalidArgument(output, requestID, "--platform-id must be a positive integer")
			}
			deployment.PlatformID = int32(parsed)
		default:
			return writeInvalidArgument(output, requestID, "unsupported profile configure flag")
		}
		args = args[2:]
	}
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, profileName)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	item, err := profile.Configure(paths.ConfigFile, deployment)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return writeInvalidArgument(output, requestID, "profile does not exist; import a token first")
		}
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	payload, err := json.Marshal(struct {
		ProfileID  string `json:"profile_id"`
		Configured bool   `json:"configured"`
	}{ProfileID: item.Name, Configured: true})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	response := contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: requestID, OK: true, Data: payload, Meta: &contracts.Meta{ProfileID: item.Name}}
	if err := cli.WriteResponse(output, format, response); err != nil {
		return 1
	}
	return 0
}

func runDaemonVerify(ctx context.Context, args []string, output io.Writer, roots commandRoots, profileName, requestID string, format cli.Output) int {
	if len(args) != 1 || args[0] != "--allow-plaintext-credentials" {
		return writeInvalidArgument(output, requestID, "daemon verify requires --allow-plaintext-credentials")
	}
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
	if !item.Deployment.Configured() {
		return writeInvalidArgument(output, requestID, "profile deployment is not configured")
	}
	credentials, err := profile.NewFileStore(roots.dataDir, true)
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
		return writeDaemonVerifyFailure(output, format, requestID)
	}
	manager, err := bridge.NewLoginMgr(prepared.SDKFactory(), paths.LockFile, nil)
	if err != nil {
		return writeDaemonVerifyFailure(output, format, requestID)
	}
	if err := manager.Start(ctx); err != nil {
		_ = manager.Shutdown(context.Background())
		return writeDaemonVerifyFailure(output, format, requestID)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		return writeDaemonVerifyFailure(output, format, requestID)
	}
	payload, err := json.Marshal(struct {
		ProfileID string `json:"profile_id"`
		Verified  bool   `json:"verified"`
	}{ProfileID: item.Name, Verified: true})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	response := contracts.Response{APIVersion: contracts.APIVersionV1, RequestID: requestID, OK: true, Data: payload, Meta: &contracts.Meta{ProfileID: item.Name}}
	if err := cli.WriteResponse(output, format, response); err != nil {
		return 1
	}
	return 0
}

// runDaemonServe composes the fixed daemon path for local Codex development.
// A same-UID provider cannot enforce the product isolation boundary, so this
// entrypoint requires an explicit acknowledgement until an isolated launcher
// is available.
func runDaemonServe(ctx context.Context, args []string, output io.Writer, roots commandRoots, profileName, requestID string, format cli.Output) int {
	options, err := parseDaemonServeOptions(args)
	if err != nil {
		return writeInvalidArgument(output, requestID, err.Error())
	}
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
	if err := validateCodexHome(options.codexHome, paths); err != nil {
		return writeInvalidArgument(output, requestID, err.Error())
	}

	credentials, err := profile.NewFileStore(roots.dataDir, true)
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
	services, err := daemon.NewUnverifiedOwnerServices(item.Name)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	ownerMethods, err := daemon.OwnerMethods(services)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	dispatcher, err := daemon.NewDispatcher(item.Name, ownerMethods)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	codex, err := codexprovider.New(codexprovider.Config{
		WorkingDir:  paths.ProviderDir,
		Environment: codexEnvironment(options.codexHome),
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	singleProvider, err := provider.New(codex)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	runs, err := runmanager.NewManager(runmanager.Config{Provider: singleProvider, MaxQueue: 2, Deadline: 2 * time.Minute})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	replies, err := reply.New(store, prepared.Adapter)
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	inbound, err := daemon.New(daemon.Config{
		ProfileID: item.Name,
		Ledger:    ledger,
		Replies:   replies,
		Runs:      runs,
		Grants:    grant.NewStore(),
		Methods:   serviceMethods(services),
		Policy: daemon.PolicyFunc(func(context.Context, contracts.Event) (daemon.Decision, bool, error) {
			return daemon.Decision{Principal: "codex", Methods: []string{"message.history"}, RateBudget: 1}, true, nil
		}),
		GrantTTL: 2 * time.Minute,
		Accept:   acceptAllInbound(item.Deployment.UserID),
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	runtime, err := daemon.NewRuntime(daemon.RuntimeConfig{
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

type daemonServeOptions struct {
	codexHome string
}

func parseDaemonServeOptions(args []string) (daemonServeOptions, error) {
	var options daemonServeOptions
	allowPlaintext := false
	allowInbound := false
	allowUnsafeProvider := false
	for len(args) > 0 {
		switch args[0] {
		case "--allow-plaintext-credentials":
			allowPlaintext = true
			args = args[1:]
		case "--allow-all-inbound":
			allowInbound = true
			args = args[1:]
		case "--allow-unsafe-same-user-provider":
			allowUnsafeProvider = true
			args = args[1:]
		case "--codex-home":
			if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
				return daemonServeOptions{}, errors.New("--codex-home requires an absolute directory")
			}
			options.codexHome = args[1]
			args = args[2:]
		default:
			return daemonServeOptions{}, errors.New("unsupported daemon serve flag")
		}
	}
	if !allowPlaintext {
		return daemonServeOptions{}, errors.New("daemon serve requires --allow-plaintext-credentials")
	}
	if !allowInbound {
		return daemonServeOptions{}, errors.New("daemon serve requires --allow-all-inbound")
	}
	if !allowUnsafeProvider {
		return daemonServeOptions{}, errors.New("daemon serve requires --allow-unsafe-same-user-provider until an isolated provider launcher is available")
	}
	if strings.TrimSpace(options.codexHome) == "" {
		return daemonServeOptions{}, errors.New("daemon serve requires --codex-home")
	}
	return options, nil
}

func validateCodexHome(home string, paths profile.Paths) error {
	if !filepath.IsAbs(home) {
		return errors.New("--codex-home requires an absolute directory")
	}
	info, err := os.Stat(home)
	if err != nil || !info.IsDir() {
		return errors.New("--codex-home must name an existing directory")
	}
	for _, privatePath := range []string{paths.DataDir, filepath.Dir(paths.ConfigFile), paths.RuntimeDir} {
		if pathContains(privatePath, home) || pathContains(home, privatePath) {
			return errors.New("--codex-home must not overlap daemon-owned profile paths")
		}
	}
	return nil
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func codexEnvironment(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Dir(home),
		"CODEX_HOME=" + home,
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

func writeDaemonVerifyFailure(output io.Writer, format cli.Output, requestID string) int {
	response := cli.ErrorResponse(requestID, contracts.CodeConnectionUnavailable, errors.New("daemon verification failed"))
	if err := cli.WriteResponse(output, format, response); err != nil {
		return 1
	}
	return cli.ExitCode(response)
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
	response := cli.ErrorResponse(requestID, contracts.CodeInvalidArgument, errorsNew(message))
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

func errorsNew(message string) error { return fmt.Errorf("%s", message) }
