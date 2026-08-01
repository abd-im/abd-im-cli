// Package codex runs the fixed Codex CLI app-server provider adapter.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/launcher"
	mcpprovider "github.com/abd-im/abd-im-cli/internal/mcp/provider"
)

const defaultInitializeTimeout = 30 * time.Second

// Config fixes the Codex executable, its isolated working directory, and the
// complete process environment. Environment must be explicitly composed so
// daemon credentials and paths are never inherited by the provider process.
type Config struct {
	Executable        string
	WorkingDir        string
	Environment       []string
	BridgeCommand     string
	Launcher          launcher.Runner
	InitializeTimeout time.Duration
}

// Adapter starts one Codex app-server process for every restricted run.
type Adapter struct {
	config    Config
	codexHome string
}

func New(config Config) (*Adapter, error) {
	if strings.TrimSpace(config.Executable) == "" || !filepath.IsAbs(config.Executable) {
		return nil, errors.New("absolute Codex executable is required")
	}
	if strings.TrimSpace(config.WorkingDir) == "" || !filepath.IsAbs(config.WorkingDir) {
		return nil, errors.New("absolute Codex working directory is required")
	}
	if len(config.Environment) == 0 {
		return nil, errors.New("explicit Codex environment is required")
	}
	if strings.TrimSpace(config.BridgeCommand) == "" || !filepath.IsAbs(config.BridgeCommand) {
		return nil, errors.New("absolute provider MCP bridge command is required")
	}
	if config.Launcher == nil {
		return nil, errors.New("isolated provider launcher is required")
	}
	codexHome, ok := environmentValue(config.Environment, "CODEX_HOME")
	if !ok || !filepath.IsAbs(codexHome) {
		return nil, errors.New("absolute CODEX_HOME is required")
	}
	if info, err := os.Stat(codexHome); err != nil || !info.IsDir() {
		return nil, errors.New("CODEX_HOME must name an existing directory")
	}
	if config.InitializeTimeout <= 0 {
		config.InitializeTimeout = defaultInitializeTimeout
	}
	return &Adapter{config: config, codexHome: codexHome}, nil
}

func (a *Adapter) Start(ctx context.Context, request contracts.StartRequest) (contracts.Session, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, errors.New("Codex provider context is unavailable")
	}
	if strings.TrimSpace(request.ProfileID) == "" || !validRunID(request.RunID) || request.Proxy == nil {
		return nil, errors.New("Codex provider start request is invalid")
	}
	runPaths, err := a.prepareRun(request)
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(runPaths.root) }
	tools := mcpprovider.DefaultTools(request.AllowedMethods)
	mcpServer, err := mcpprovider.New(request.ProfileID, request.GrantCredential, request.Proxy, tools)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create provider MCP server: %w", err)
	}
	mcpBridge, err := mcpprovider.StartBridge(runPaths.socket, mcpServer)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("start provider MCP bridge: %w", err)
	}
	if err := a.config.Launcher.PrepareSocket(runPaths.socket); err != nil {
		_ = mcpBridge.Close()
		cleanup()
		return nil, fmt.Errorf("prepare provider MCP bridge: %w", err)
	}
	executable, err := exec.LookPath(a.config.Executable)
	if err != nil {
		_ = mcpBridge.Close()
		cleanup()
		return nil, errors.New("Codex executable is unavailable")
	}
	processContext, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(processContext, executable, "app-server", "--listen", "stdio://")
	command.Dir = runPaths.workDir
	command.Env = runEnvironment(a.config.Environment, runPaths.home, runPaths.workDir)
	configureProcessGroup(command)
	if err := a.config.Launcher.Configure(command); err != nil {
		cancel()
		_ = mcpBridge.Close()
		cleanup()
		return nil, fmt.Errorf("configure isolated provider process: %w", err)
	}

	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		_ = mcpBridge.Close()
		cleanup()
		return nil, errors.New("create Codex stdin pipe")
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		_ = mcpBridge.Close()
		cleanup()
		return nil, errors.New("create Codex stdout pipe")
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		cancel()
		_ = mcpBridge.Close()
		cleanup()
		return nil, fmt.Errorf("start Codex app-server: %w", err)
	}

	session := &session{
		command: command,
		stdin:   stdin,
		cancel:  cancel,
		pending: make(map[int]chan rpcResult),
		done:    make(chan struct{}),
		bridge:  mcpBridge,
		cleanup: cleanup,
	}
	go session.read(stdout)
	go session.wait()

	initializeContext, initializeCancel := context.WithTimeout(ctx, a.config.InitializeTimeout)
	defer initializeCancel()
	if _, err := session.request(initializeContext, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "abdim-cli", "version": contracts.APIVersionV1},
		"capabilities": map[string]any{},
	}); err != nil {
		_ = session.Close(context.Background())
		return nil, errors.New("initialize Codex app-server")
	}
	if err := session.notify("initialized", nil); err != nil {
		_ = session.Close(context.Background())
		return nil, errors.New("notify Codex app-server initialization")
	}
	thread, err := session.request(initializeContext, "thread/start", map[string]any{
		"model":                  nil,
		"modelProvider":          nil,
		"profile":                nil,
		"cwd":                    runPaths.workDir,
		"approvalPolicy":         "never",
		"sandbox":                "read-only",
		"config":                 nil,
		"baseInstructions":       nil,
		"developerInstructions":  nil,
		"compactPrompt":          nil,
		"includeApplyPatchTool":  false,
		"experimentalRawEvents":  false,
		"persistExtendedHistory": false,
	})
	if err != nil {
		_ = session.Close(context.Background())
		return nil, errors.New("start Codex thread")
	}
	threadID := nestedString(thread, "thread", "id")
	if threadID == "" {
		_ = session.Close(context.Background())
		return nil, errors.New("Codex app-server returned no thread ID")
	}
	session.mu.Lock()
	session.threadID = threadID
	session.mu.Unlock()
	return session, nil
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

type turnState struct {
	done  chan struct{}
	final string
	err   error
}

type session struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	cancel  context.CancelFunc

	mu       sync.Mutex
	writeMu  sync.Mutex
	nextID   int
	pending  map[int]chan rpcResult
	threadID string
	turn     *turnState
	closed   bool
	waitErr  error
	done     chan struct{}
	close    sync.Once
	bridge   *mcpprovider.Bridge
	cleanup  func()
}

func (s *session) Turn(ctx context.Context, request contracts.TurnRequest) (contracts.TurnResult, error) {
	if ctx == nil || ctx.Err() != nil || strings.TrimSpace(request.Prompt) == "" {
		return contracts.TurnResult{}, errors.New("Codex turn request is invalid")
	}
	s.mu.Lock()
	if s.closed || s.threadID == "" || s.turn != nil {
		s.mu.Unlock()
		return contracts.TurnResult{}, errors.New("Codex session is unavailable")
	}
	turn := &turnState{done: make(chan struct{})}
	threadID := s.threadID
	s.turn = turn
	s.mu.Unlock()

	_, err := s.request(ctx, "turn/start", map[string]any{
		"threadId": threadID,
		"input":    []map[string]string{{"type": "text", "text": request.Prompt}},
	})
	if err != nil {
		s.finishTurn(turn, "", errors.New("Codex turn start failed"))
	}
	select {
	case <-turn.done:
		s.mu.Lock()
		s.turn = nil
		final, turnErr := turn.final, turn.err
		s.mu.Unlock()
		if turnErr != nil {
			return contracts.TurnResult{}, turnErr
		}
		if strings.TrimSpace(final) == "" {
			return contracts.TurnResult{}, errors.New("Codex turn returned no final answer")
		}
		return contracts.TurnResult{FinalText: final, SessionRef: threadID}, nil
	case <-ctx.Done():
		_ = s.Cancel(context.Background())
		return contracts.TurnResult{}, ctx.Err()
	}
}

// Cancel stops the app-server rather than approving an arbitrary provider
// action. The run manager will create a fresh session after this interruption.
func (s *session) Cancel(context.Context) error {
	s.closeProcess()
	return nil
}

func (s *session) Close(ctx context.Context) error {
	s.closeProcess()
	if ctx == nil {
		return errors.New("close context is required")
	}
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) closeProcess() {
	s.close.Do(func() {
		s.mu.Lock()
		s.closed = true
		turn := s.turn
		s.mu.Unlock()
		if turn != nil {
			s.finishTurn(turn, "", errors.New("Codex turn interrupted"))
		}
		_ = s.stdin.Close()
		if s.bridge != nil {
			_ = s.bridge.Close()
		}
		terminateProcessGroup(s.command)
		s.cancel()
	})
}

func (s *session) read(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024), 1<<20)
	for scanner.Scan() {
		s.handle(strings.TrimSpace(scanner.Text()))
	}
	if scanner.Err() != nil {
		s.closeProcess()
	}
}

func (s *session) wait() {
	err := s.command.Wait()
	if s.bridge != nil {
		_ = s.bridge.Close()
	}
	if s.cleanup != nil {
		s.cleanup()
	}
	s.mu.Lock()
	s.waitErr = err
	for _, pending := range s.pending {
		pending <- rpcResult{err: errors.New("Codex app-server exited")}
	}
	s.pending = make(map[int]chan rpcResult)
	turn := s.turn
	s.mu.Unlock()
	if turn != nil {
		s.finishTurn(turn, "", errors.New("Codex app-server exited"))
	}
	close(s.done)
}

func (s *session) request(ctx context.Context, method string, params any) (map[string]any, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("Codex app-server is closed")
	}
	s.nextID++
	id := s.nextID
	pending := make(chan rpcResult, 1)
	s.pending[id] = pending
	s.mu.Unlock()
	if err := s.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}
	select {
	case result := <-pending:
		if result.err != nil {
			return nil, result.err
		}
		var payload map[string]any
		if json.Unmarshal(result.result, &payload) != nil {
			return nil, errors.New("Codex app-server returned invalid JSON")
		}
		return payload, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ctx.Err()
	case <-s.done:
		return nil, errors.New("Codex app-server exited")
	}
}

func (s *session) notify(method string, params any) error {
	message := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		message["params"] = params
	}
	return s.write(message)
}

func (s *session) write(message map[string]any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.stdin.Write(payload)
	return err
}

func (s *session) handle(line string) {
	if line == "" {
		return
	}
	var message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(line), &message) != nil {
		return
	}
	if len(message.ID) != 0 && message.Method == "" {
		var id int
		if json.Unmarshal(message.ID, &id) != nil {
			return
		}
		s.mu.Lock()
		pending := s.pending[id]
		delete(s.pending, id)
		s.mu.Unlock()
		if pending == nil {
			return
		}
		if message.Error != nil {
			pending <- rpcResult{err: errors.New("Codex app-server rejected request")}
			return
		}
		pending <- rpcResult{result: append(json.RawMessage(nil), message.Result...)}
		return
	}
	if len(message.ID) != 0 && message.Method != "" {
		s.handleServerRequest(message.ID, message.Method)
		return
	}
	if message.Method != "" {
		s.handleNotification(message.Method, message.Params)
	}
}

func (s *session) handleServerRequest(id json.RawMessage, method string) {
	var requestID int
	if json.Unmarshal(id, &requestID) != nil {
		return
	}
	// The provider has no direct command or file authority. Declining keeps
	// this adapter useful for text replies without expanding its privileges.
	if method == "item/commandExecution/requestApproval" || method == "item/fileChange/requestApproval" {
		_ = s.write(map[string]any{"jsonrpc": "2.0", "id": requestID, "result": map[string]string{"decision": "decline"}})
		return
	}
	_ = s.write(map[string]any{"jsonrpc": "2.0", "id": requestID, "error": map[string]any{"code": -32601, "message": "method not supported"}})
}

func (s *session) handleNotification(method string, raw json.RawMessage) {
	var params map[string]any
	if json.Unmarshal(raw, &params) != nil {
		return
	}
	s.mu.Lock()
	threadID := s.threadID
	turn := s.turn
	s.mu.Unlock()
	if incoming, _ := params["threadId"].(string); incoming != "" && threadID != "" && incoming != threadID {
		return
	}
	if turn == nil {
		return
	}
	switch method {
	case "item/completed":
		item, _ := params["item"].(map[string]any)
		itemType, _ := item["type"].(string)
		phase, _ := item["phase"].(string)
		if itemType == "agentMessage" && phase == "final_answer" {
			text, _ := item["text"].(string)
			if text != "" {
				s.mu.Lock()
				if s.turn == turn {
					turn.final = text
				}
				s.mu.Unlock()
			}
		}
	case "turn/completed":
		status := nestedString(params, "turn", "status")
		if status == "failed" || status == "cancelled" || status == "canceled" || status == "aborted" || status == "interrupted" {
			s.finishTurn(turn, "", errors.New("Codex turn did not complete"))
			return
		}
		s.finishTurn(turn, "", nil)
	case "thread/status/changed":
		if nestedString(params, "status", "type") == "idle" {
			s.finishTurn(turn, "", nil)
		}
	}
}

func (s *session) finishTurn(turn *turnState, final string, err error) {
	s.mu.Lock()
	if s.turn == turn && final != "" {
		turn.final = final
	}
	if s.turn == turn && err != nil {
		turn.err = err
	}
	s.mu.Unlock()
	select {
	case <-turn.done:
	default:
		close(turn.done)
	}
}

func nestedString(value map[string]any, keys ...string) string {
	var current any = value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	text, _ := current.(string)
	return text
}

type runPathSet struct {
	root    string
	home    string
	workDir string
	socket  string
}

func (a *Adapter) prepareRun(request contracts.StartRequest) (runPathSet, error) {
	paths := runPathSet{
		root:    filepath.Join(a.config.WorkingDir, request.RunID),
		home:    filepath.Join(a.config.WorkingDir, request.RunID, "codex"),
		workDir: filepath.Join(a.config.WorkingDir, request.RunID, "work"),
		socket:  filepath.Join(a.config.WorkingDir, request.RunID, "mcp.sock"),
	}
	if err := os.RemoveAll(paths.root); err != nil {
		return runPathSet{}, fmt.Errorf("remove previous Codex run directory: %w", err)
	}
	cleanup := func(err error) (runPathSet, error) {
		_ = os.RemoveAll(paths.root)
		return runPathSet{}, err
	}
	for _, directory := range []string{paths.root, paths.home, paths.workDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return cleanup(fmt.Errorf("create Codex run directory: %w", err))
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return cleanup(fmt.Errorf("secure Codex run directory: %w", err))
		}
	}
	if err := a.config.Launcher.CopyCodexAuth(filepath.Join(paths.home, "auth.json")); err != nil {
		return cleanup(fmt.Errorf("copy Codex credentials: %w", err))
	}
	if err := writeRunConfig(filepath.Join(paths.home, "config.toml"), a.config.BridgeCommand, paths.socket, mcpprovider.DefaultTools(request.AllowedMethods)); err != nil {
		return cleanup(fmt.Errorf("write provider MCP configuration: %w", err))
	}
	if err := a.config.Launcher.PrepareRun(paths.root, paths.home, paths.workDir); err != nil {
		return cleanup(fmt.Errorf("prepare isolated Codex run: %w", err))
	}
	return paths, nil
}

func writeRunConfig(path, command, socket string, tools []mcpprovider.Tool) error {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	encodedNames, err := json.Marshal(names)
	if err != nil {
		return err
	}
	args, err := json.Marshal([]string{"mcp", "provider", "bridge", "--socket", socket})
	if err != nil {
		return err
	}
	config := "[history]\npersistence = \"none\"\n\n" +
		"[mcp_servers.abdim]\n" +
		"command = " + strconv.Quote(command) + "\n" +
		"args = " + string(args) + "\n" +
		"enabled_tools = " + string(encodedNames) + "\n" +
		"required = true\n" +
		"default_tools_approval_mode = \"auto\"\n"
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func runEnvironment(source []string, home, workDir string) []string {
	result := make([]string, 0, len(source)+2)
	for _, value := range source {
		if strings.HasPrefix(value, "HOME=") || strings.HasPrefix(value, "CODEX_HOME=") {
			continue
		}
		result = append(result, value)
	}
	return append(result, "HOME="+workDir, "CODEX_HOME="+home)
}

func environmentValue(environment []string, name string) (string, bool) {
	prefix := name + "="
	for _, value := range environment {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix), true
		}
	}
	return "", false
}

func validRunID(value string) bool {
	if len(value) == 0 || len(value) > 64 || filepath.Base(value) != value {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

var _ contracts.Provider = (*Adapter)(nil)
var _ contracts.Session = (*session)(nil)
