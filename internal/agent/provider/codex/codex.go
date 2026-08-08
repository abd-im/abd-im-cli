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

	"github.com/abd-im/abd-im-cli/internal/agent/access"
	"github.com/abd-im/abd-im-cli/internal/contracts"
)

const defaultInitializeTimeout = 30 * time.Second

// Config fixes the Codex executable, its isolated working directory, and the
// complete process environment. Environment must be explicitly composed so
// daemon credentials and paths are never inherited by the provider process.
type Config struct {
	Executable        string
	WorkingDir        string
	StateDir          string
	SourceCodexHome   string
	Environment       []string
	CLICommand        string
	InitializeTimeout time.Duration
}

// Adapter starts one Codex app-server process for every restricted run.
type Adapter struct {
	config          Config
	stateDir        string
	sourceCodexHome string
}

func New(config Config) (*Adapter, error) {
	if strings.TrimSpace(config.Executable) == "" || !filepath.IsAbs(config.Executable) {
		return nil, errors.New("absolute Codex executable is required")
	}
	if strings.TrimSpace(config.WorkingDir) == "" || !filepath.IsAbs(config.WorkingDir) {
		return nil, errors.New("absolute Codex working directory is required")
	}
	if config.StateDir == "" {
		config.StateDir = filepath.Join(config.WorkingDir, "state")
	}
	if !filepath.IsAbs(config.StateDir) {
		return nil, errors.New("absolute Codex state directory is required")
	}
	if strings.TrimSpace(config.SourceCodexHome) == "" || !filepath.IsAbs(config.SourceCodexHome) {
		return nil, errors.New("absolute source CODEX_HOME is required")
	}
	if info, err := os.Stat(config.SourceCodexHome); err != nil || !info.IsDir() {
		return nil, errors.New("source CODEX_HOME must name an existing directory")
	}
	if len(config.Environment) == 0 {
		return nil, errors.New("explicit Codex environment is required")
	}
	if strings.TrimSpace(config.CLICommand) == "" || !filepath.IsAbs(config.CLICommand) {
		return nil, errors.New("absolute abdim CLI command is required")
	}
	if config.InitializeTimeout <= 0 {
		config.InitializeTimeout = defaultInitializeTimeout
	}
	return &Adapter{config: config, stateDir: config.StateDir, sourceCodexHome: config.SourceCodexHome}, nil
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
	accessServer, err := access.Listen(runPaths.socket, request.Proxy)
	if err != nil {
		cleanup()
		return nil, err
	}
	executable, err := exec.LookPath(a.config.Executable)
	if err != nil {
		_ = accessServer.Close()
		cleanup()
		return nil, errors.New("Codex executable is unavailable")
	}
	processContext, cancel := context.WithCancel(context.Background())
	environment, err := access.Environment(runEnvironment(a.config.Environment, runPaths.home, runPaths.workDir), a.config.CLICommand, access.Context{
		Socket: runPaths.socket, ProfileID: request.ProfileID, RunID: request.RunID,
		Grant: request.GrantCredential, AllowedMethods: request.AllowedMethods,
	})
	if err != nil {
		cancel()
		_ = accessServer.Close()
		cleanup()
		return nil, err
	}
	command := exec.CommandContext(processContext, executable, "app-server", "--listen", "stdio://")
	command.Dir = runPaths.workDir
	command.Env = environment
	configureProcessGroup(command)

	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		_ = accessServer.Close()
		cleanup()
		return nil, errors.New("create Codex stdin pipe")
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		_ = accessServer.Close()
		cleanup()
		return nil, errors.New("create Codex stdout pipe")
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		cancel()
		_ = accessServer.Close()
		cleanup()
		return nil, fmt.Errorf("start Codex app-server: %w", err)
	}

	session := &session{
		command: command,
		stdin:   stdin,
		cancel:  cancel,
		pending: make(map[int]chan rpcResult),
		done:    make(chan struct{}),
		access:  accessServer,
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
	settings, err := configuredThreadSettings(filepath.Join(a.stateDir, "config.toml"))
	if err != nil {
		_ = session.Close(context.Background())
		return nil, err
	}
	method := "thread/start"
	params := threadParams(runPaths.workDir, settings)
	if request.SessionRef != "" {
		method = "thread/resume"
		params["threadId"] = request.SessionRef
	}
	thread, err := session.request(initializeContext, method, params)
	if err != nil && request.SessionRef != "" && isThreadNotFound(err) {
		_ = session.Close(context.Background())
		return nil, contracts.ErrSessionNotFound
	}
	if err != nil {
		_ = session.Close(context.Background())
		return nil, errors.New("start or resume Codex thread")
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

type threadSettings struct {
	model         *string
	modelProvider *string
}

func threadParams(workDir string, settings threadSettings) map[string]any {
	return map[string]any{
		"model":                  settings.model,
		"modelProvider":          settings.modelProvider,
		"profile":                nil,
		"cwd":                    workDir,
		"approvalPolicy":         "never",
		"sandbox":                "danger-full-access",
		"config":                 nil,
		"baseInstructions":       nil,
		"developerInstructions":  nil,
		"compactPrompt":          nil,
		"includeApplyPatchTool":  false,
		"experimentalRawEvents":  false,
		"persistExtendedHistory": false,
	}
}

// configuredThreadSettings reads only the two top-level values accepted by
// the app-server's thread start and resume requests. Resuming must include
// them explicitly so a thread does not retain a provider that is no longer
// selected in the current profile configuration.
func configuredThreadSettings(path string) (threadSettings, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return threadSettings{}, fmt.Errorf("read Codex run configuration: %w", err)
	}
	var settings threadSettings
	for _, line := range strings.Split(string(payload), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) < 2 || (value[0] != '"' && value[0] != '\'') || value[len(value)-1] != value[0] {
			continue
		}
		parsed := value[1 : len(value)-1]
		if value[0] == '"' {
			var err error
			parsed, err = strconv.Unquote(value)
			if err != nil {
				continue
			}
		}
		switch strings.TrimSpace(key) {
		case "model":
			settings.model = &parsed
		case "model_provider":
			settings.modelProvider = &parsed
		}
	}
	return settings, nil
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

type rpcError struct {
	code    int
	message string
}

func (e *rpcError) Error() string { return "Codex app-server rejected request" }

func isThreadNotFound(err error) bool {
	var rpcErr *rpcError
	return errors.As(err, &rpcErr) && rpcErr.code == -32600 && strings.HasPrefix(rpcErr.message, "no rollout found for thread id ")
}

type turnState struct {
	ctx                context.Context
	output             contracts.TurnOutputSink
	activity           contracts.TurnActivitySink
	done               chan struct{}
	agentMessagePhases map[string]string
	agentMessageText   map[string]string
	text               string
	final              string
	err                error
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
	access   *access.Server
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
	turn := &turnState{
		ctx: ctx, output: request.Output, activity: request.Activity, done: make(chan struct{}),
		agentMessagePhases: make(map[string]string), agentMessageText: make(map[string]string),
	}
	threadID := s.threadID
	s.turn = turn
	s.mu.Unlock()

	_, err := s.request(ctx, "turn/start", map[string]any{
		"threadId": threadID,
		"input":    []map[string]string{{"type": "text", "text": request.Prompt}},
	})
	if err != nil {
		s.finishTurn(turn, "", fmt.Errorf("Codex turn start failed: %w", err))
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
		if s.access != nil {
			_ = s.access.Close()
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
	if s.access != nil {
		_ = s.access.Close()
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
			Code    int    `json:"code"`
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
			pending <- rpcResult{err: &rpcError{code: message.Error.Code, message: message.Error.Message}}
			return
		}
		pending <- rpcResult{result: append(json.RawMessage(nil), message.Result...)}
		return
	}
	if len(message.ID) != 0 && message.Method != "" {
		s.handleServerRequest(message.ID, message.Method, message.Params)
		return
	}
	if message.Method != "" {
		s.handleNotification(message.Method, message.Params)
	}
}

func (s *session) handleServerRequest(id json.RawMessage, method string, params json.RawMessage) {
	var requestID int
	if json.Unmarshal(id, &requestID) != nil {
		return
	}
	if method == "item/commandExecution/requestApproval" || method == "item/fileChange/requestApproval" {
		_ = s.write(map[string]any{"jsonrpc": "2.0", "id": requestID, "result": map[string]string{"decision": "accept"}})
		return
	}
	if method == "item/permissions/requestApproval" {
		approvalID := fmt.Sprintf("permission-%d", requestID)
		s.mu.Lock()
		turn := s.turn
		s.mu.Unlock()
		if turn != nil {
			s.deliverActivity(turn, contracts.TurnActivity{
				Kind: "approval.requested", RequestID: approvalID, Name: "permission",
				Summary: permissionSummary(params),
			})
		}
		_ = s.write(map[string]any{"jsonrpc": "2.0", "id": requestID, "result": permissionApproval(params)})
		if turn != nil {
			s.deliverActivity(turn, contracts.TurnActivity{
				Kind: "approval.resolved", RequestID: approvalID, Decision: "accepted",
			})
		}
		return
	}
	_ = s.write(map[string]any{"jsonrpc": "2.0", "id": requestID, "error": map[string]any{"code": -32601, "message": "method not supported"}})
}

func permissionApproval(raw json.RawMessage) map[string]any {
	var request struct {
		Permissions map[string]any `json:"permissions"`
	}
	_ = json.Unmarshal(raw, &request)
	permissions := make(map[string]any, 2)
	for _, key := range []string{"network", "fileSystem"} {
		if value := request.Permissions[key]; value != nil {
			permissions[key] = value
		}
	}
	return map[string]any{"permissions": permissions, "scope": "turn"}
}

func permissionSummary(raw json.RawMessage) string {
	var request struct {
		Permissions map[string]any `json:"permissions"`
	}
	_ = json.Unmarshal(raw, &request)
	parts := make([]string, 0, 2)
	if request.Permissions["network"] != nil {
		parts = append(parts, "Network access")
	}
	if request.Permissions["fileSystem"] != nil {
		parts = append(parts, "File access")
	}
	return strings.Join(parts, ", ")
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
	case "item/started":
		item, _ := params["item"].(map[string]any)
		s.rememberAgentMessagePhase(turn, item)
		if activity, ok := codexToolActivity(item, "tool.started"); ok {
			s.deliverActivity(turn, activity)
		}
	case "item/agentMessage/delta":
		s.deliverAgentMessageDelta(turn, params)
	case "item/completed":
		item, _ := params["item"].(map[string]any)
		itemType, _ := item["type"].(string)
		phase, _ := item["phase"].(string)
		s.rememberAgentMessagePhase(turn, item)
		if itemType == "reasoning" {
			if summary := codexReasoningSummary(item); summary != "" {
				s.deliverActivity(turn, contracts.TurnActivity{Kind: "activity.summary", Summary: summary})
			}
		}
		if itemType == "agentMessage" && phase == "commentary" {
			text, _ := item["text"].(string)
			if summary := boundedSummary(text); summary != "" {
				s.deliverActivity(turn, contracts.TurnActivity{Kind: "activity.summary", Summary: summary})
			}
		}
		if activity, ok := codexToolActivity(item, "tool.completed"); ok {
			s.deliverActivity(turn, activity)
		}
		if itemType == "agentMessage" && phase == "final_answer" {
			text, _ := item["text"].(string)
			if text != "" {
				s.deliver(turn, text)
				s.mu.Lock()
				turn.final = text
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
	case "error":
		willRetry, _ := params["willRetry"].(bool)
		if !willRetry {
			s.finishTurn(turn, "", codexTurnError(params))
		}
	case "thread/status/changed":
		if nestedString(params, "status", "type") == "idle" {
			s.finishTurn(turn, "", nil)
		}
	}
}

func codexTurnError(params map[string]any) error {
	message := nestedString(params, "error", "message")
	if message == "" {
		message, _ = params["message"].(string)
	}
	if message == "" {
		return errors.New("Codex turn did not complete")
	}
	return fmt.Errorf("Codex turn did not complete: %s", boundedSummary(message))
}

func codexToolActivity(item map[string]any, kind string) (contracts.TurnActivity, bool) {
	itemType, _ := item["type"].(string)
	name := map[string]string{
		"commandExecution": "shell",
		"fileChange":       "file_change",
		"mcpToolCall":      "mcp",
		"webSearch":        "web_search",
		"imageView":        "image_view",
	}[itemType]
	callID, _ := item["id"].(string)
	if name == "" || callID == "" {
		return contracts.TurnActivity{}, false
	}
	status, _ := item["status"].(string)
	if kind == "tool.started" {
		status = "running"
	} else if status == "" {
		status = "completed"
	}
	summary, _ := item["summary"].(string)
	if summary == "" {
		for _, key := range []string{"command", "query", "tool", "path"} {
			if value, _ := item[key].(string); value != "" {
				summary = value
				break
			}
		}
	}
	return contracts.TurnActivity{
		Kind:       kind,
		CallID:     callID,
		Name:       name,
		Summary:    boundedSummary(summary),
		Status:     status,
		DurationMS: numericInt64(item["durationMs"]),
	}, true
}

// Codex exposes summary as the approved, user-visible form of reasoning.
// Raw reasoning content is deliberately never forwarded to the workspace.
func codexReasoningSummary(item map[string]any) string {
	parts, _ := item["summary"].([]any)
	text := make([]string, 0, len(parts))
	for _, part := range parts {
		if value, _ := part.(string); value != "" {
			text = append(text, value)
		}
	}
	return boundedSummary(strings.Join(text, " "))
}

func boundedSummary(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 500 {
		value = string(runes[:500])
	}
	return value
}

func numericInt64(value any) int64 {
	switch number := value.(type) {
	case float64:
		return int64(number)
	case int64:
		return number
	case int:
		return int64(number)
	default:
		return 0
	}
}

func (s *session) rememberAgentMessagePhase(turn *turnState, item map[string]any) {
	itemType, _ := item["type"].(string)
	itemID, _ := item["id"].(string)
	phase, _ := item["phase"].(string)
	if itemType != "agentMessage" || itemID == "" || phase == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turn == turn {
		turn.agentMessagePhases[itemID] = phase
	}
}

func (s *session) deliverAgentMessageDelta(turn *turnState, params map[string]any) {
	itemID, _ := params["itemId"].(string)
	delta, _ := params["delta"].(string)
	if itemID == "" || delta == "" {
		return
	}
	s.mu.Lock()
	if s.turn != turn || turn.err != nil || turn.agentMessagePhases[itemID] != "final_answer" {
		s.mu.Unlock()
		return
	}
	text := turn.agentMessageText[itemID] + delta
	turn.agentMessageText[itemID] = text
	s.mu.Unlock()
	s.deliver(turn, text)
}

func (s *session) deliver(turn *turnState, text string) {
	s.mu.Lock()
	if s.turn != turn || turn.err != nil || text == turn.text {
		s.mu.Unlock()
		return
	}
	turn.text = text
	output, outputContext := turn.output, turn.ctx
	s.mu.Unlock()
	if output != nil {
		if err := output(outputContext, contracts.TurnOutput{Text: text}); err != nil {
			s.mu.Lock()
			if s.turn == turn && turn.err == nil {
				turn.err = err
			}
			s.mu.Unlock()
			_ = s.Cancel(context.Background())
		}
	}
}

func (s *session) deliverActivity(turn *turnState, activity contracts.TurnActivity) {
	s.mu.Lock()
	if s.turn != turn || turn.err != nil {
		s.mu.Unlock()
		return
	}
	sink, activityContext := turn.activity, turn.ctx
	s.mu.Unlock()
	if sink == nil {
		return
	}
	if err := sink(activityContext, activity); err != nil {
		s.mu.Lock()
		if s.turn == turn && turn.err == nil {
			turn.err = err
		}
		s.mu.Unlock()
		_ = s.Cancel(context.Background())
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
		home:    a.stateDir,
		workDir: filepath.Join(a.config.WorkingDir, request.RunID, "work"),
		socket:  filepath.Join(a.config.WorkingDir, request.RunID, "work", ".abdim.sock"),
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
	if err := copyCodexAuth(filepath.Join(a.sourceCodexHome, "auth.json"), filepath.Join(paths.home, "auth.json")); err != nil {
		return cleanup(fmt.Errorf("copy Codex credentials: %w", err))
	}
	if err := writeRunConfig(filepath.Join(a.sourceCodexHome, "config.toml"), filepath.Join(paths.home, "config.toml")); err != nil {
		return cleanup(fmt.Errorf("write Codex run configuration: %w", err))
	}
	return paths, nil
}

func copyCodexAuth(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("current user Codex login is unavailable")
	}
	payload, err := os.ReadFile(source)
	if err != nil || len(payload) == 0 || len(payload) > 1<<20 {
		return errors.New("read current user Codex login")
	}
	if err := os.WriteFile(destination, payload, 0o600); err != nil {
		return err
	}
	return os.Chmod(destination, 0o600)
}

func writeRunConfig(sourcePath, path string) error {
	base, err := inheritedConfig(sourcePath)
	if err != nil {
		return err
	}
	config := base + "[history]\npersistence = \"none\"\n\n" +
		"[shell_environment_policy]\n" +
		"inherit = \"all\"\n" +
		"ignore_default_excludes = true\n" +
		"include_only = [\"PATH\", \"ABDIM_CLI\", \"ABDIM_AGENT_SOCKET\", \"ABDIM_AGENT_PROFILE\", \"ABDIM_AGENT_RUN\", \"ABDIM_AGENT_GRANT\", \"ABDIM_AGENT_METHODS\"]\n"
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// inheritedConfig keeps top-level model settings and model provider tables.
// Every other source table is excluded so history and shell environment
// propagation are controlled by this adapter.
func inheritedConfig(path string) (string, error) {
	payload, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read current Codex configuration: %w", err)
	}
	if len(payload) > 1<<20 {
		return "", errors.New("current Codex configuration is too large")
	}
	var builder strings.Builder
	inModelProvider := false
	seenTable := false
	for _, line := range strings.Split(string(payload), "\n") {
		trimmed := strings.TrimSpace(line)
		isHeader := strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")
		if isHeader {
			if inModelProvider {
				builder.WriteString("supports_websockets = false\n")
			}
			seenTable = true
			inModelProvider = isModelProviderTableHeader(trimmed)
		}
		if !seenTable || inModelProvider {
			if inModelProvider && strings.HasPrefix(trimmed, "supports_websockets") && strings.Contains(trimmed, "=") {
				continue
			}
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
	}
	if inModelProvider {
		builder.WriteString("supports_websockets = false\n")
	}
	return builder.String(), nil
}

func isModelProviderTableHeader(line string) bool {
	name := strings.TrimSpace(line)
	if strings.HasPrefix(name, "[[") && strings.HasSuffix(name, "]]") {
		name = strings.TrimSpace(name[2 : len(name)-2])
	} else if strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]") {
		name = strings.TrimSpace(name[1 : len(name)-1])
	} else {
		return false
	}
	return strings.HasPrefix(name, "model_providers.")
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
