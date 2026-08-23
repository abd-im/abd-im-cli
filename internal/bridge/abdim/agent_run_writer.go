package abdim

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/google/uuid"
)

const (
	agentRunStreamType = "agent_run_v2"
	maxRunPacketBytes  = 16*1024 - 1
	maxRunTotalBytes   = 128*1024 - 1
	terminalReserve    = 1024
)

type AgentRunStream interface {
	Queued(context.Context) error
	Started(context.Context) error
	Append(context.Context, contracts.RunEvent) error
	Finish(context.Context, RunFinish) error
}

type AgentRunSender interface {
	StartAgentRun(context.Context, contracts.AgentRunMetadata, string, string) (AgentRunStream, error)
}

type RunFinish struct {
	Outcome    string
	Reason     string
	ErrorCode  string
	DurationMS int64
}

type AgentRunWriter struct {
	user           userContext
	globalConfig   *ccontext.GlobalConfig
	conversationID string
	clientMsgID    string

	mu         sync.Mutex
	nextIndex  int64
	totalBytes int
	state      string
	startedAt  time.Time
}

func (a *Adapter) StartAgentRun(ctx context.Context, metadata contracts.AgentRunMetadata, recipientID, groupID string) (AgentRunStream, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if metadata.Schema != contracts.AgentRunSchema || metadata.SchemaVersion != contracts.AgentRunSchemaVersion || strings.TrimSpace(metadata.RunID) == "" || strings.TrimSpace(metadata.TriggerMessageID) == "" {
		return nil, errors.New("invalid Agent run metadata")
	}
	if (strings.TrimSpace(recipientID) == "") == (strings.TrimSpace(groupID) == "") {
		return nil, errors.New("invalid Agent run target")
	}
	content, err := json.Marshal(metadata)
	if err != nil {
		return nil, errors.New("marshal Agent run metadata")
	}
	user, err := a.currentUser()
	if err != nil {
		return nil, err
	}
	config := a.config
	sendContext := ccontext.WithInfo(ctx, &ccontext.GlobalConfig{UserID: a.userID, Token: a.token, IMConfig: &config})
	clientMsgID := uuid.NewString()
	callback := newSendCallback()
	conversationID, err := user.StartStreamMessage(
		ccontext.WithOperationID(sendContext, uuid.NewString()), callback, agentRunStreamType,
		string(content), clientMsgID, recipientID, groupID,
	)
	if err != nil {
		return nil, errors.New("OpenIM Agent run submission failed")
	}
	select {
	case err := <-callback.done:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		return nil, errors.New("OpenIM Agent run outcome is unknown")
	}
	return &AgentRunWriter{
		user: user, globalConfig: &ccontext.GlobalConfig{UserID: a.userID, Token: a.token, IMConfig: &config},
		conversationID: conversationID, clientMsgID: clientMsgID,
	}, nil
}

func (w *AgentRunWriter) Queued(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state != "" {
		return errors.New("Agent run is already queued")
	}
	event := contracts.NewRunLifecycleEvent("run.queued", time.Now().UnixMilli())
	if err := w.appendLocked(ctx, event, false); err != nil {
		return err
	}
	w.state = "queued"
	return nil
}

func (w *AgentRunWriter) Started(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state != "queued" {
		return errors.New("Agent run cannot start from current state")
	}
	event := contracts.NewRunLifecycleEvent("run.started", time.Now().UnixMilli())
	if err := w.appendLocked(ctx, event, false); err != nil {
		return err
	}
	w.state = "started"
	w.startedAt = time.Now()
	return nil
}

func (w *AgentRunWriter) Append(ctx context.Context, event contracts.RunEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state != "started" {
		return errors.New("Agent run does not accept content in current state")
	}
	switch event.(type) {
	case contracts.RunLifecycleEvent:
		return errors.New("Agent run lifecycle is writer-owned")
	}
	return w.appendLocked(ctx, event, false)
}

func (w *AgentRunWriter) Finish(ctx context.Context, finish RunFinish) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state != "queued" && w.state != "started" {
		return errors.New("Agent run cannot finish from current state")
	}
	event := contracts.NewRunLifecycleEvent("run.finished", time.Now().UnixMilli())
	event.Outcome = finish.Outcome
	event.Reason = finish.Reason
	event.ErrorCode = finish.ErrorCode
	event.DurationMS = finish.DurationMS
	if event.DurationMS == 0 && !w.startedAt.IsZero() {
		event.DurationMS = time.Since(w.startedAt).Milliseconds()
	}
	if err := w.appendLocked(ctx, event, true); err != nil {
		return err
	}
	w.state = "finished"
	return nil
}

func (w *AgentRunWriter) appendLocked(ctx context.Context, event contracts.RunEvent, end bool) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := contracts.ValidateRunEvent(event); err != nil {
		return err
	}
	packet, err := json.Marshal(event)
	if err != nil {
		return errors.New("marshal Agent run event")
	}
	limit := maxRunTotalBytes
	if !end {
		limit -= terminalReserve
	}
	if len(packet) > maxRunPacketBytes || w.totalBytes+len(packet) > limit {
		return errors.New("Agent run stream capacity exceeded")
	}
	if err := w.user.AppendStreamMessage(
		ccontext.WithOperationID(ccontext.WithInfo(ctx, w.globalConfig), uuid.NewString()), w.conversationID,
		w.clientMsgID, w.nextIndex, []string{string(packet)}, end,
	); err != nil {
		return errors.New("OpenIM Agent run outcome is unknown")
	}
	w.nextIndex++
	w.totalBytes += len(packet)
	return nil
}
