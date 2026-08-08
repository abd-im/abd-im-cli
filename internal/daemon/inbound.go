// Package daemon composes the in-process P1 inbound path.
package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/grant"
	"github.com/abd-im/abd-im-cli/internal/agent/proxy"
	"github.com/abd-im/abd-im-cli/internal/agent/run"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/events"
	"github.com/abd-im/abd-im-cli/internal/reply"
)

var ErrStopped = errors.New("daemon inbound path is stopped")

// Policy decides whether one persisted inbound message may start an automatic
// provider run. The daemon derives the conversation target and message window;
// policy cannot replace either value.
type Policy interface {
	Decide(context.Context, InboundContext) (Decision, bool, error)
}

type PolicyFunc func(context.Context, InboundContext) (Decision, bool, error)

func (f PolicyFunc) Decide(ctx context.Context, inbound InboundContext) (Decision, bool, error) {
	return f(ctx, inbound)
}

// InboundContext contains the authenticated SDK event identity used by policy.
// Message text is intentionally excluded so authorization never depends on the
// prompt body.
type InboundContext struct {
	Event            contracts.Event
	SenderID         string
	ConversationID   string
	GroupID          string
	SessionType      int32
	ContentType      int32
	SenderPlatformID int32
	ConversationKind contracts.ConversationKind
}

// Decision selects a subset of the daemon's fixed typed tool registry.
type Decision struct {
	Principal            string
	Methods              []string
	HistoryBeforeTrigger bool
	AttachmentByteLimit  int64
	RateBudget           int
}

type Config struct {
	ProfileID           string
	Ledger              *events.Ledger
	Replies             *reply.Service
	Runs                *run.Manager
	Grants              *grant.Store
	Methods             []proxy.Method
	Policy              Policy
	WorkspaceClassifier contracts.ConversationClassifier

	GrantTTL time.Duration
	OnError  func(error)
}

// Inbound accepts normalized SDK events and owns their progression from the
// durable ledger to a run-private proxy and event-bound reply.
type Inbound struct {
	profileID           string
	ledger              *events.Ledger
	replies             *reply.Service
	runs                *run.Manager
	grants              *grant.Store
	methods             map[string]proxy.Method
	policy              Policy
	workspaceClassifier contracts.ConversationClassifier
	grantTTL            time.Duration
	onError             func(error)

	mu          sync.Mutex
	stopped     bool
	runsByEvent map[string]string
	finishers   sync.WaitGroup
}

// Outcome reports whether an event was ignored, deduplicated, or accepted as
// a provider run. It never contains a grant credential or message body.
type Outcome struct {
	EventID string
	RunID   string
	Created bool
	Ignored bool
}

func New(config Config) (*Inbound, error) {
	if strings.TrimSpace(config.ProfileID) == "" || config.Ledger == nil || config.Replies == nil || config.Runs == nil || config.Grants == nil || config.Policy == nil {
		return nil, errors.New("profile ID, ledger, replies, runs, grants, and policy are required")
	}
	if config.GrantTTL <= 0 {
		return nil, errors.New("positive grant TTL is required")
	}
	methods := make(map[string]proxy.Method, len(config.Methods))
	for _, method := range config.Methods {
		if strings.TrimSpace(method.Name) == "" || method.Handle == nil {
			return nil, errors.New("typed method name and handler are required")
		}
		if _, exists := methods[method.Name]; exists {
			return nil, fmt.Errorf("duplicate typed method %q", method.Name)
		}
		methods[method.Name] = method
	}
	return &Inbound{
		profileID:           config.ProfileID,
		ledger:              config.Ledger,
		replies:             config.Replies,
		runs:                config.Runs,
		grants:              config.Grants,
		methods:             methods,
		policy:              config.Policy,
		workspaceClassifier: config.WorkspaceClassifier,
		grantTTL:            config.GrantTTL,
		onError:             config.OnError,
		runsByEvent:         make(map[string]string),
	}, nil
}

// Listener is suitable for bridge.NewLoginMgr. It only copies and schedules
// callback work; failures are reported through the configured error sink.
func (d *Inbound) Listener(ctx context.Context, event contracts.SDKEvent) {
	event.Data = append(json.RawMessage(nil), event.Data...)
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	go func() {
		if _, err := d.Process(ctx, event); err != nil {
			d.report(err)
		}
	}()
}

// Process handles one normalized event synchronously. It is exported for
// deterministic daemon tests; production SDK callbacks use Listener.
func (d *Inbound) Process(ctx context.Context, event contracts.SDKEvent) (Outcome, error) {
	if err := event.Validate(); err != nil {
		return Outcome{}, err
	}
	if event.ProfileID != d.profileID {
		return Outcome{}, errors.New("event profile does not match daemon")
	}
	if d.isStopped() {
		return Outcome{}, ErrStopped
	}

	reference := eventReference(event.Data)
	recorded, err := d.ledger.RecordCallback(ctx, events.Callback{
		ProfileID:      event.ProfileID,
		DedupKey:       event.DedupKey,
		Type:           event.Type,
		ConversationID: reference.ConversationID,
		MessageID:      reference.MessageID,
		OccurredAt:     event.OccurredAt,
	})
	if err != nil {
		return Outcome{}, err
	}
	outcome := Outcome{EventID: recorded.Event.EventID, Created: recorded.Created}
	if !recorded.Created || event.Type != string(contracts.EventMessageReceived) {
		outcome.Ignored = true
		return outcome, nil
	}
	if err := reference.validate(); err != nil {
		return outcome, err
	}
	conversationKind := d.conversationKind(ctx, reference.GroupID)
	decision, allowed, err := d.policy.Decide(ctx, InboundContext{
		Event:            recorded.Event,
		SenderID:         reference.SenderID,
		ConversationID:   reference.ConversationID,
		GroupID:          reference.GroupID,
		SessionType:      reference.SessionType,
		ContentType:      reference.ContentType,
		SenderPlatformID: reference.SenderPlatformID,
		ConversationKind: conversationKind,
	})
	if err != nil {
		return outcome, err
	}
	if !allowed {
		outcome.Ignored = true
		return outcome, nil
	}
	conversation := reference
	target, err := conversation.replyTarget()
	if err != nil {
		return outcome, err
	}
	selected, err := d.selectMethods(decision.Methods)
	if err != nil {
		return outcome, err
	}
	if strings.TrimSpace(decision.Principal) == "" || decision.RateBudget <= 0 || decision.AttachmentByteLimit < 0 {
		return outcome, errors.New("policy must set a principal, positive rate budget, and non-negative attachment byte limit")
	}

	runID := newRunID()
	if _, err := d.replies.Reserve(ctx, reply.Binding{
		ProfileID:        d.profileID,
		EventID:          recorded.Event.EventID,
		ConversationID:   conversation.ConversationID,
		TriggerMessageID: conversation.MessageID,
		RecipientID:      target.recipientID,
		GroupID:          target.groupID,
		RunID:            runID,
	}); err != nil {
		return outcome, err
	}
	workspace := conversationKind == contracts.ConversationKindAgentWorkspace
	var stream *reply.Stream
	var agentReply *agentRunReply
	if workspace {
		agentStream, streamErr := d.replies.NewAgentRun(ctx, d.profileID, recorded.Event.EventID)
		if streamErr != nil {
			return outcome, streamErr
		}
		agentReply = &agentRunReply{run: agentStream}
	} else {
		stream, err = d.replies.NewStream(ctx, d.profileID, recorded.Event.EventID)
		if err != nil {
			return outcome, err
		}
	}
	messageWindow := grant.MessageWindow{
		ConversationID: conversation.ConversationID,
		AfterMessageID: conversation.MessageID,
	}
	if decision.HistoryBeforeTrigger {
		messageWindow.AfterMessageID = ""
		messageWindow.BeforeMessageID = conversation.MessageID
	}
	issued, credential, err := d.grants.Issue(grant.Policy{
		RunID:               runID,
		ProfileID:           d.profileID,
		Principal:           decision.Principal,
		Methods:             methodNames(selected),
		MessageWindow:       messageWindow,
		AttachmentByteLimit: decision.AttachmentByteLimit,
		ExpiresAt:           time.Now().Add(d.grantTTL),
		RateBudget:          decision.RateBudget,
	})
	if err != nil {
		return outcome, err
	}
	toolProxy, err := proxy.New(d.grants, runID, d.profileID, selected)
	if err != nil {
		d.grants.RevokeRun(runID)
		return outcome, err
	}
	allowedMethods := providerVisibleMethods(selected)
	request := run.Request{
		ID:              runID,
		ProfileID:       d.profileID,
		ConversationID:  conversation.ConversationID,
		EventID:         recorded.Event.EventID,
		GrantCredential: credential,
		GrantExpiresAt:  issued.ExpiresAt,
		AllowedMethods:  allowedMethods,
		Proxy:           toolProxy,
		Prompt:          inboundPrompt(event.MessageText, allowedMethods),
	}
	if workspace {
		request.Output = agentReply.output
		request.Activity = agentReply.activity
		request.Started = agentReply.ensureStarted
	} else {
		request.Output = func(outputContext context.Context, output contracts.TurnOutput) error {
			return stream.Update(outputContext, output.Text)
		}
	}
	if agentReply != nil {
		if err := agentReply.run.Queued(ctx); err != nil {
			_ = toolProxy.Close(context.Background())
			d.grants.RevokeRun(runID)
			return outcome, err
		}
	}
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		_ = toolProxy.Close(context.Background())
		d.grants.RevokeRun(runID)
		if agentReply != nil {
			_ = agentReply.fail(context.WithoutCancel(ctx), "Agent run was interrupted")
		}
		return outcome, ErrStopped
	}
	handle, err := d.runs.Submit(request)
	if err != nil {
		d.mu.Unlock()
		_ = toolProxy.Close(context.Background())
		d.grants.RevokeRun(runID)
		if agentReply != nil {
			_ = agentReply.fail(context.WithoutCancel(ctx), "Agent run could not be queued")
		}
		return outcome, err
	}
	d.runsByEvent[recorded.Event.EventID] = runID
	d.finishers.Add(1)
	d.mu.Unlock()
	outcome.RunID = runID
	go func() {
		defer d.finishers.Done()
		d.finish(recorded.Event.EventID, handle, stream, agentReply)
	}()
	return outcome, nil
}

// CancelEvent prevents the corresponding run from producing an event-bound
// reply and revokes its run-private proxy credential.
func (d *Inbound) CancelEvent(eventID string) bool {
	d.mu.Lock()
	runID, exists := d.runsByEvent[eventID]
	d.mu.Unlock()
	return exists && d.runs.Cancel(runID)
}

func (d *Inbound) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	d.stopped = true
	d.mu.Unlock()
	if err := d.runs.Shutdown(ctx); err != nil {
		return err
	}
	d.finishers.Wait()
	return nil
}

func (d *Inbound) finish(eventID string, handle *run.Handle, stream *reply.Stream, agentReply *agentRunReply) {
	result, ok := <-handle.Done
	d.mu.Lock()
	delete(d.runsByEvent, eventID)
	d.mu.Unlock()
	if agentReply != nil {
		if !ok {
			if err := agentReply.fail(context.Background(), "Agent run was interrupted"); err != nil {
				d.report(err)
			}
			return
		}
		if err := agentReply.finish(context.Background(), result); err != nil {
			d.report(err)
		}
		return
	}
	if !ok || result.Status != run.StatusCompleted || strings.TrimSpace(result.Turn.FinalText) == "" {
		if err := stream.Close(context.Background()); err != nil {
			d.report(err)
		}
		return
	}
	if err := stream.Finish(context.Background(), result.Turn.FinalText); err != nil {
		d.report(err)
	}
}

func (d *Inbound) conversationKind(ctx context.Context, groupID string) contracts.ConversationKind {
	if d.workspaceClassifier == nil || strings.TrimSpace(groupID) == "" {
		return contracts.ConversationKindChat
	}
	kind, err := d.workspaceClassifier.ConversationKind(ctx, groupID)
	if err != nil || kind != contracts.ConversationKindAgentWorkspace {
		return contracts.ConversationKindChat
	}
	return kind
}

type agentRunReply struct {
	run      *reply.AgentRun
	start    sync.Once
	startErr error
}

func (r *agentRunReply) ensureStarted(ctx context.Context) error {
	r.start.Do(func() { r.startErr = r.run.Started(ctx) })
	return r.startErr
}

func (r *agentRunReply) output(ctx context.Context, output contracts.TurnOutput) error {
	writeContext := context.WithoutCancel(ctx)
	if err := r.ensureStarted(writeContext); err != nil {
		return err
	}
	return r.run.Answer(writeContext, output.Text)
}

func (r *agentRunReply) activity(ctx context.Context, activity contracts.TurnActivity) error {
	writeContext := context.WithoutCancel(ctx)
	if err := r.ensureStarted(writeContext); err != nil {
		return err
	}
	return r.run.Activity(writeContext, activity)
}

func (r *agentRunReply) finish(ctx context.Context, result run.Result) error {
	if err := r.ensureStarted(ctx); err != nil {
		return err
	}
	switch result.Status {
	case run.StatusCompleted:
		if strings.TrimSpace(result.Turn.FinalText) != "" {
			if err := r.run.Answer(ctx, result.Turn.FinalText); err != nil {
				return err
			}
		}
		return r.run.Complete(ctx)
	case run.StatusCanceled:
		return r.run.Cancel(ctx)
	default:
		return r.run.Fail(ctx, "Agent run failed")
	}
}

func (r *agentRunReply) fail(ctx context.Context, summary string) error {
	if err := r.ensureStarted(ctx); err != nil {
		return err
	}
	return r.run.Fail(ctx, summary)
}

func (d *Inbound) selectMethods(names []string) ([]proxy.Method, error) {
	selected := make([]proxy.Method, 0, len(names))
	methodSeen := make(map[string]struct{}, len(names))
	for _, name := range names {
		method, exists := d.methods[name]
		if !exists {
			return nil, fmt.Errorf("policy selected unregistered typed method %q", name)
		}
		if _, exists := methodSeen[name]; exists {
			return nil, fmt.Errorf("policy selected duplicate typed method %q", name)
		}
		methodSeen[name] = struct{}{}
		selected = append(selected, method)
	}
	return selected, nil
}

type eventRef struct {
	ConversationID   string `json:"conversation_id"`
	MessageID        string `json:"message_id"`
	SenderID         string `json:"sender_id,omitempty"`
	GroupID          string `json:"group_id,omitempty"`
	SessionType      int32  `json:"session_type,omitempty"`
	ContentType      int32  `json:"content_type,omitempty"`
	SenderPlatformID int32  `json:"sender_platform_id,omitempty"`
}

func eventReference(raw json.RawMessage) eventRef {
	var reference eventRef
	_ = json.Unmarshal(raw, &reference)
	return reference
}

func (reference eventRef) validate() error {
	if strings.TrimSpace(reference.ConversationID) == "" || strings.TrimSpace(reference.MessageID) == "" || strings.TrimSpace(reference.SenderID) == "" {
		return errors.New("message event requires sender, conversation, and message references")
	}
	return nil
}

type replyTarget struct {
	recipientID string
	groupID     string
}

func (reference eventRef) replyTarget() (replyTarget, error) {
	switch reference.SessionType {
	case 1:
		if strings.TrimSpace(reference.SenderID) != "" {
			return replyTarget{recipientID: reference.SenderID}, nil
		}
	case 2, 3:
		if strings.TrimSpace(reference.GroupID) != "" {
			return replyTarget{groupID: reference.GroupID}, nil
		}
	}
	return replyTarget{}, errors.New("message event has no safe reply target")
}

func inboundPrompt(text string, allowedMethods []string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "An inbound non-text message was received. Reply briefly that only text messages are supported."
	}
	prefix := "Reply concisely and helpfully to this inbound message. Do not claim to have accessed IM data or performed actions you did not perform. Never disclose local paths, configuration, credentials, grants, or other runtime details."
	if len(allowedMethods) > 0 {
		prefix += " Use only the abdim CLI for IM data or actions. Run `\"$ABDIM_CLI\" commands` to inspect this run's allowed commands and JSON parameter schemas. Invoke a command as `printf '%s' '<json>' | \"$ABDIM_CLI\" <method words> --params-stdin`. If a required command is unavailable, say that it is unavailable rather than guessing."
	}
	return prefix + "\n\nInbound message:\n" + text
}

func methodNames(methods []proxy.Method) []string {
	names := make([]string, 0, len(methods))
	for _, method := range methods {
		names = append(names, method.Name)
	}
	return names
}

// providerVisibleMethods freezes the discovery surface before a provider is
// started. The run proxy still enforces method access, expiry, revocation, and
// rate limits for every call after this snapshot is made.
func providerVisibleMethods(methods []proxy.Method) []string {
	return methodNames(methods)
}

func (d *Inbound) isStopped() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stopped
}

func (d *Inbound) report(err error) {
	if err != nil && d.onError != nil {
		d.onError(err)
	}
}

func newRunID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}
