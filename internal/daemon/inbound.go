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

	"github.com/abd-im-cli/abdim-cli/internal/agent/grant"
	"github.com/abd-im-cli/abdim-cli/internal/agent/proxy"
	"github.com/abd-im-cli/abdim-cli/internal/agent/run"
	"github.com/abd-im-cli/abdim-cli/internal/contracts"
	"github.com/abd-im-cli/abdim-cli/internal/events"
	"github.com/abd-im-cli/abdim-cli/internal/reply"
)

var ErrStopped = errors.New("daemon inbound path is stopped")

// Policy decides whether one persisted inbound message may start an automatic
// provider run. The daemon derives the conversation target and message window;
// policy cannot replace either value.
type Policy interface {
	Decide(context.Context, contracts.Event) (Decision, bool, error)
}

type PolicyFunc func(context.Context, contracts.Event) (Decision, bool, error)

func (f PolicyFunc) Decide(ctx context.Context, event contracts.Event) (Decision, bool, error) {
	return f(ctx, event)
}

// Decision selects a subset of the daemon's fixed typed tool registry.
type Decision struct {
	Principal  string
	Methods    []string
	RateBudget int
}

type Config struct {
	ProfileID string
	Ledger    *events.Ledger
	Replies   *reply.Service
	Runs      *run.Manager
	Grants    *grant.Store
	Methods   []proxy.Method
	Policy    Policy

	GrantTTL time.Duration
	OnError  func(error)
}

// Inbound accepts normalized SDK events and owns their progression from the
// durable ledger to a run-private proxy and event-bound reply.
type Inbound struct {
	profileID string
	ledger    *events.Ledger
	replies   *reply.Service
	runs      *run.Manager
	grants    *grant.Store
	methods   map[string]proxy.Method
	policy    Policy
	grantTTL  time.Duration
	onError   func(error)

	mu          sync.Mutex
	stopped     bool
	runsByEvent map[string]string
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
		if strings.TrimSpace(method.Name) == "" || strings.TrimSpace(method.Scope) == "" || method.Handle == nil {
			return nil, errors.New("typed method name, scope, and handler are required")
		}
		if _, exists := methods[method.Name]; exists {
			return nil, fmt.Errorf("duplicate typed method %q", method.Name)
		}
		methods[method.Name] = method
	}
	return &Inbound{
		profileID:   config.ProfileID,
		ledger:      config.Ledger,
		replies:     config.Replies,
		runs:        config.Runs,
		grants:      config.Grants,
		methods:     methods,
		policy:      config.Policy,
		grantTTL:    config.GrantTTL,
		onError:     config.OnError,
		runsByEvent: make(map[string]string),
	}, nil
}

// Listener is suitable for bridge.NewLoginMgr. It only copies and schedules
// callback work; failures are reported through the configured error sink.
func (d *Inbound) Listener(ctx context.Context, event contracts.Event) {
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
func (d *Inbound) Process(ctx context.Context, event contracts.Event) (Outcome, error) {
	if err := event.Validate(); err != nil {
		return Outcome{}, err
	}
	if event.ProfileID != d.profileID {
		return Outcome{}, errors.New("event profile does not match daemon")
	}
	if d.isStopped() {
		return Outcome{}, ErrStopped
	}

	recorded, err := d.ledger.RecordCallback(ctx, events.Callback{
		ProfileID:      event.ProfileID,
		DedupKey:       event.DedupKey,
		Type:           event.Type,
		ConversationID: eventReference(event.Data).ConversationID,
		MessageID:      eventReference(event.Data).MessageID,
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

	decision, allowed, err := d.policy.Decide(ctx, event)
	if err != nil {
		return outcome, err
	}
	if !allowed {
		outcome.Ignored = true
		return outcome, nil
	}
	conversation, err := referenceFromEvent(recorded.Event)
	if err != nil {
		return outcome, err
	}
	selected, scopes, err := d.selectMethods(decision.Methods)
	if err != nil {
		return outcome, err
	}
	if strings.TrimSpace(decision.Principal) == "" || decision.RateBudget <= 0 {
		return outcome, errors.New("policy must set a principal and positive rate budget")
	}

	runID := newRunID()
	if _, err := d.replies.Reserve(ctx, reply.Binding{
		ProfileID:        d.profileID,
		EventID:          recorded.Event.EventID,
		ConversationID:   conversation.ConversationID,
		TriggerMessageID: conversation.MessageID,
		RunID:            runID,
	}); err != nil {
		return outcome, err
	}
	issued, credential, err := d.grants.Issue(grant.Policy{
		RunID:           runID,
		ProfileID:       d.profileID,
		Principal:       decision.Principal,
		Methods:         methodNames(selected),
		Scopes:          scopes,
		TargetAllowlist: []string{conversation.ConversationID},
		MessageWindow: grant.MessageWindow{
			ConversationID: conversation.ConversationID,
			AfterMessageID: conversation.MessageID,
		},
		ExpiresAt:  time.Now().Add(d.grantTTL),
		RateBudget: decision.RateBudget,
	})
	if err != nil {
		return outcome, err
	}
	toolProxy, err := proxy.New(d.grants, runID, d.profileID, selected)
	if err != nil {
		d.grants.RevokeRun(runID)
		return outcome, err
	}
	handle, err := d.runs.Submit(run.Request{
		ID:              runID,
		ProfileID:       d.profileID,
		ConversationID:  conversation.ConversationID,
		EventID:         recorded.Event.EventID,
		GrantCredential: credential,
		GrantExpiresAt:  issued.ExpiresAt,
		Proxy:           toolProxy,
	})
	if err != nil {
		return outcome, err
	}
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		d.runs.Cancel(runID)
		return outcome, ErrStopped
	}
	d.runsByEvent[recorded.Event.EventID] = runID
	d.mu.Unlock()
	outcome.RunID = runID
	go d.finish(recorded.Event.EventID, handle)
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
	return d.runs.Shutdown(ctx)
}

func (d *Inbound) finish(eventID string, handle *run.Handle) {
	result, ok := <-handle.Done
	d.mu.Lock()
	delete(d.runsByEvent, eventID)
	stopped := d.stopped
	d.mu.Unlock()
	if !ok || stopped || result.Status != run.StatusCompleted || strings.TrimSpace(result.Turn.FinalText) == "" {
		return
	}
	if _, err := d.replies.Deliver(context.Background(), d.profileID, eventID, result.Turn.FinalText); err != nil {
		d.report(err)
	}
}

func (d *Inbound) selectMethods(names []string) ([]proxy.Method, []string, error) {
	if len(names) == 0 {
		return nil, nil, errors.New("policy must select at least one typed method")
	}
	selected := make([]proxy.Method, 0, len(names))
	methodSeen := make(map[string]struct{}, len(names))
	scopeSeen := make(map[string]struct{}, len(names))
	scopes := make([]string, 0, len(names))
	for _, name := range names {
		method, exists := d.methods[name]
		if !exists {
			return nil, nil, fmt.Errorf("policy selected unregistered typed method %q", name)
		}
		if _, exists := methodSeen[name]; exists {
			return nil, nil, fmt.Errorf("policy selected duplicate typed method %q", name)
		}
		methodSeen[name] = struct{}{}
		selected = append(selected, method)
		if _, exists := scopeSeen[method.Scope]; !exists {
			scopeSeen[method.Scope] = struct{}{}
			scopes = append(scopes, method.Scope)
		}
	}
	return selected, scopes, nil
}

type eventRef struct {
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
}

func eventReference(raw json.RawMessage) eventRef {
	var reference eventRef
	_ = json.Unmarshal(raw, &reference)
	return reference
}

func referenceFromEvent(event contracts.Event) (eventRef, error) {
	reference := eventReference(event.Data)
	if strings.TrimSpace(reference.ConversationID) == "" || strings.TrimSpace(reference.MessageID) == "" {
		return eventRef{}, errors.New("message event requires conversation and message references")
	}
	return reference, nil
}

func methodNames(methods []proxy.Method) []string {
	names := make([]string, 0, len(methods))
	for _, method := range methods {
		names = append(names, method.Name)
	}
	return names
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
