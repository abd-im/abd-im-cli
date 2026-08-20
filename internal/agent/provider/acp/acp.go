// Package acp adapts the ACP Go SDK to abdim's provider contract.
package acp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

const defaultInitializeTimeout = 30 * time.Second

var ErrProtocolUnsupported = errors.New("ACP protocol version is unsupported")

type Config struct {
	Executable        string
	Args              []string
	WorkingDir        string
	Environment       []string
	CLICommand        string
	InitializeTimeout time.Duration
}

type Adapter struct {
	config Config
}

func New(config Config) (*Adapter, error) {
	if strings.TrimSpace(config.Executable) == "" || !filepath.IsAbs(config.Executable) {
		return nil, errors.New("absolute ACP Agent executable is required")
	}
	if strings.TrimSpace(config.WorkingDir) == "" || !filepath.IsAbs(config.WorkingDir) {
		return nil, errors.New("absolute ACP working directory is required")
	}
	if len(config.Environment) == 0 {
		return nil, errors.New("explicit ACP Agent environment is required")
	}
	if strings.TrimSpace(config.CLICommand) == "" || !filepath.IsAbs(config.CLICommand) {
		return nil, errors.New("absolute abdim CLI command is required")
	}
	for _, arg := range config.Args {
		if strings.ContainsRune(arg, 0) {
			return nil, errors.New("ACP Agent argument contains NUL")
		}
	}
	if config.InitializeTimeout <= 0 {
		config.InitializeTimeout = defaultInitializeTimeout
	}
	config.Args = append([]string(nil), config.Args...)
	config.Environment = append([]string(nil), config.Environment...)
	return &Adapter{config: config}, nil
}

func (a *Adapter) Start(ctx context.Context, request contracts.StartRequest) (contracts.Session, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, errors.New("ACP provider context is unavailable")
	}
	if strings.TrimSpace(request.ProfileID) == "" || !validRunID(request.RunID) {
		return nil, errors.New("ACP provider start request is invalid")
	}
	paths, err := prepareRun(a.config.WorkingDir, request.RunID)
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(paths.root) }
	executable, err := exec.LookPath(a.config.Executable)
	if err != nil {
		cleanup()
		return nil, errors.New("ACP Agent executable is unavailable")
	}

	processContext, processCancel := context.WithCancel(context.Background())
	environment := append(append([]string(nil), a.config.Environment...), "ABDIM_CLI="+a.config.CLICommand)
	command := exec.CommandContext(processContext, executable, a.config.Args...)
	command.Dir = paths.work
	command.Env = environment
	command.Stderr = io.Discard
	configureProcessGroup(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		processCancel()
		cleanup()
		return nil, errors.New("create ACP stdin pipe")
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		processCancel()
		cleanup()
		return nil, errors.New("create ACP stdout pipe")
	}
	if err := command.Start(); err != nil {
		processCancel()
		cleanup()
		return nil, fmt.Errorf("start ACP Agent: %w", err)
	}

	session := &session{
		command: command,
		stdin:   stdin,
		cancel:  processCancel,
		done:    make(chan struct{}),
		cleanup: cleanup,
	}
	client := &client{session: session}
	session.connection = acpsdk.NewClientSideConnection(client, stdin, stdout)
	session.connection.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go session.wait()

	initializeContext, initializeCancel := context.WithTimeout(ctx, a.config.InitializeTimeout)
	defer initializeCancel()
	initialized, err := session.connection.Initialize(initializeContext, acpsdk.InitializeRequest{
		ProtocolVersion:    acpsdk.ProtocolVersionNumber,
		ClientCapabilities: acpsdk.ClientCapabilities{},
		ClientInfo: &acpsdk.Implementation{
			Name: "abdim-cli", Title: acpsdk.Ptr("ABD IM CLI"), Version: contracts.APIVersionV1,
		},
	})
	if err != nil {
		_ = session.Close(context.Background())
		return nil, errors.New("initialize ACP Agent")
	}
	if initialized.ProtocolVersion != acpsdk.ProtocolVersionNumber {
		_ = session.Close(context.Background())
		return nil, fmt.Errorf("%w: negotiated %d", ErrProtocolUnsupported, initialized.ProtocolVersion)
	}
	session.mu.Lock()
	session.supportsClose = initialized.AgentCapabilities.SessionCapabilities.Close != nil
	session.supportsLoad = initialized.AgentCapabilities.LoadSession
	session.mu.Unlock()

	var sessionID acpsdk.SessionId
	if request.SessionRef != "" {
		if !initialized.AgentCapabilities.LoadSession {
			_ = session.Close(context.Background())
			return nil, contracts.ErrSessionNotFound
		}
		sessionID = acpsdk.SessionId(request.SessionRef)
		_, err = session.connection.LoadSession(initializeContext, acpsdk.LoadSessionRequest{
			Cwd: paths.work, McpServers: []acpsdk.McpServer{}, SessionId: sessionID,
		})
		if isSessionNotFound(err) {
			_ = session.Close(context.Background())
			return nil, contracts.ErrSessionNotFound
		}
		if err != nil {
			_ = session.Close(context.Background())
			return nil, errors.New("load ACP session")
		}
	} else {
		created, createErr := session.connection.NewSession(initializeContext, acpsdk.NewSessionRequest{Cwd: paths.work})
		if createErr != nil {
			_ = session.Close(context.Background())
			return nil, errors.New("create ACP session")
		}
		sessionID = created.SessionId
	}
	if strings.TrimSpace(string(sessionID)) == "" {
		_ = session.Close(context.Background())
		return nil, errors.New("ACP Agent returned no session ID")
	}
	session.mu.Lock()
	session.sessionID = sessionID
	session.mu.Unlock()
	return session, nil
}

func isSessionNotFound(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == -32002
}

type turnState struct {
	ctx      context.Context
	output   contracts.TurnOutputSink
	activity contracts.TurnActivitySink
	tools    map[string]contracts.TurnActivity
	text     string
	err      error
}

type session struct {
	command    *exec.Cmd
	stdin      io.Closer
	cancel     context.CancelFunc
	connection *acpsdk.ClientSideConnection
	cleanup    func()
	done       chan struct{}
	close      sync.Once

	mu            sync.Mutex
	sessionID     acpsdk.SessionId
	supportsLoad  bool
	supportsClose bool
	closed        bool
	turn          *turnState
}

func (s *session) Turn(ctx context.Context, request contracts.TurnRequest) (contracts.TurnResult, error) {
	if ctx == nil || ctx.Err() != nil || strings.TrimSpace(request.Prompt) == "" {
		return contracts.TurnResult{}, errors.New("ACP turn request is invalid")
	}
	s.mu.Lock()
	if s.closed || s.sessionID == "" || s.turn != nil {
		s.mu.Unlock()
		return contracts.TurnResult{}, errors.New("ACP session is unavailable")
	}
	turn := &turnState{ctx: ctx, output: request.Output, activity: request.Activity, tools: make(map[string]contracts.TurnActivity)}
	s.turn = turn
	sessionID := s.sessionID
	s.mu.Unlock()

	response, promptErr := s.connection.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock(request.Prompt)},
	})
	s.mu.Lock()
	if s.turn == turn {
		s.turn = nil
	}
	finalText, outputErr := turn.text, turn.err
	s.mu.Unlock()
	if outputErr != nil {
		return contracts.TurnResult{}, fmt.Errorf("deliver ACP output: %w", outputErr)
	}
	if promptErr != nil {
		return contracts.TurnResult{}, errors.New("ACP prompt failed")
	}
	if response.StopReason == acpsdk.StopReasonCancelled {
		return contracts.TurnResult{}, errors.New("ACP turn was cancelled")
	}
	if strings.TrimSpace(finalText) == "" {
		return contracts.TurnResult{}, errors.New("ACP turn returned no final answer")
	}
	result := contracts.TurnResult{FinalText: finalText}
	s.mu.Lock()
	if s.supportsLoad {
		result.SessionRef = string(sessionID)
	}
	s.mu.Unlock()
	return result, nil
}

func (s *session) Cancel(ctx context.Context) error {
	if ctx == nil {
		return errors.New("cancel context is required")
	}
	s.mu.Lock()
	sessionID, closed := s.sessionID, s.closed
	s.mu.Unlock()
	if closed || sessionID == "" {
		return nil
	}
	return s.connection.Cancel(ctx, acpsdk.CancelNotification{SessionId: sessionID})
}

func (s *session) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("close context is required")
	}
	s.mu.Lock()
	sessionID, supportsClose, closed := s.sessionID, s.supportsClose, s.closed
	s.mu.Unlock()
	if !closed && supportsClose && sessionID != "" {
		closeContext, cancel := context.WithTimeout(ctx, time.Second)
		_, _ = s.connection.CloseSession(closeContext, acpsdk.CloseSessionRequest{SessionId: sessionID})
		cancel()
	}
	s.closeProcess()
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
		s.mu.Unlock()
		_ = s.stdin.Close()
		terminateProcessGroup(s.command)
		s.cancel()
	})
}

func (s *session) wait() {
	_ = s.command.Wait()
	if s.cleanup != nil {
		s.cleanup()
	}
	close(s.done)
}

type client struct {
	session *session
}

func (c *client) SessionUpdate(_ context.Context, notification acpsdk.SessionNotification) error {
	s := c.session
	s.mu.Lock()
	turn := s.turn
	if turn == nil || notification.SessionId != s.sessionID {
		s.mu.Unlock()
		return nil
	}
	if update := notification.Update.AgentMessageChunk; update != nil && update.Content.Text != nil {
		turn.text += update.Content.Text.Text
		text, output, outputContext := turn.text, turn.output, turn.ctx
		s.mu.Unlock()
		if output == nil {
			return nil
		}
		if err := output(outputContext, contracts.TurnOutput{Text: text}); err != nil {
			s.mu.Lock()
			if s.turn == turn && turn.err == nil {
				turn.err = err
			}
			s.mu.Unlock()
			_ = s.Cancel(context.Background())
		}
		return nil
	}
	if update := notification.Update.ToolCall; update != nil {
		activity := contracts.TurnActivity{
			Kind: "tool.started", CallID: string(update.ToolCallId), Name: acpToolName(update.Kind),
			Summary: boundedACPSummary(update.Title), Status: string(update.Status),
		}
		if activity.Status == "" {
			activity.Status = "running"
		}
		turn.tools[activity.CallID] = activity
		s.mu.Unlock()
		s.deliverActivity(turn, activity)
		return nil
	}
	if update := notification.Update.ToolCallUpdate; update != nil && update.Status != nil &&
		(*update.Status == acpsdk.ToolCallStatusCompleted || *update.Status == acpsdk.ToolCallStatusFailed) {
		activity := turn.tools[string(update.ToolCallId)]
		activity.Kind = "tool.completed"
		activity.CallID = string(update.ToolCallId)
		activity.Status = string(*update.Status)
		if update.Title != nil {
			activity.Summary = boundedACPSummary(*update.Title)
		}
		if update.Kind != nil {
			activity.Name = acpToolName(*update.Kind)
		}
		turn.tools[activity.CallID] = activity
		s.mu.Unlock()
		s.deliverActivity(turn, activity)
		return nil
	}
	s.mu.Unlock()
	return nil
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

func acpToolName(kind acpsdk.ToolKind) string {
	if kind == acpsdk.ToolKindExecute {
		return "terminal"
	}
	if kind == "" {
		return "tool"
	}
	return string(kind)
}

func boundedACPSummary(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 500 {
		value = string(runes[:500])
	}
	return value
}

func (c *client) RequestPermission(_ context.Context, request acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	c.session.mu.Lock()
	sessionID := c.session.sessionID
	c.session.mu.Unlock()
	if request.SessionId != sessionID {
		return acpsdk.RequestPermissionResponse{}, acpsdk.NewInvalidParams(map[string]any{"reason": "permission session mismatch"})
	}
	for _, option := range request.Options {
		if option.Kind == acpsdk.PermissionOptionKindAllowOnce || option.Kind == acpsdk.PermissionOptionKindAllowAlways {
			return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeSelected(option.OptionId)}, nil
		}
	}
	return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeCancelled()}, nil
}

func (*client) ReadTextFile(context.Context, acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodFsReadTextFile)
}

func (*client) WriteTextFile(context.Context, acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodFsWriteTextFile)
}

func (*client) CreateTerminal(context.Context, acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalCreate)
}

func (*client) KillTerminal(context.Context, acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalKill)
}

func (*client) TerminalOutput(context.Context, acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalOutput)
}

func (*client) ReleaseTerminal(context.Context, acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalRelease)
}

func (*client) WaitForTerminalExit(context.Context, acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, acpsdk.NewMethodNotFound(acpsdk.ClientMethodTerminalWaitForExit)
}

func prepareRun(workingDir, runID string) (runPaths, error) {
	paths := runPaths{
		root: filepath.Join(workingDir, runID),
		work: filepath.Join(workingDir, runID, "work"),
	}
	if err := os.RemoveAll(paths.root); err != nil {
		return runPaths{}, fmt.Errorf("remove previous ACP run directory: %w", err)
	}
	for _, directory := range []string{paths.root, paths.work} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			_ = os.RemoveAll(paths.root)
			return runPaths{}, fmt.Errorf("create ACP run directory: %w", err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			_ = os.RemoveAll(paths.root)
			return runPaths{}, fmt.Errorf("secure ACP run directory: %w", err)
		}
	}
	return paths, nil
}

type runPaths struct {
	root string
	work string
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
var _ acpsdk.Client = (*client)(nil)
