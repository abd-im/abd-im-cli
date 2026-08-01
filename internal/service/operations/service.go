// Package operations provides owner-only diagnostics for provider runs and
// remote side-effect records. It intentionally has no provider grant access.
package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/abd-im/abd-im-cli/internal/agent/run"
	"github.com/abd-im/abd-im-cli/internal/control"
	baseservice "github.com/abd-im/abd-im-cli/internal/service"
)

const (
	RunListMethod              = "run.list"
	RunCancelMethod            = "run.cancel"
	OperationGetMethod         = "operation.get"
	OperationMarkUnknownMethod = "operation.mark_unknown"
	ownerOperationsScope       = "owner.operations"
)

var (
	ErrRunNotActive = errors.New("run is not active")
)

// Tracker is attached to a run manager. It persists only lifecycle metadata;
// run prompts, credentials, tool calls, and provider output remain transient.
type Tracker struct {
	store *control.Store

	mu       sync.Mutex
	profiles map[string]string
}

func NewTracker(store *control.Store) (*Tracker, error) {
	if store == nil {
		return nil, errors.New("control store is required")
	}
	return &Tracker{store: store, profiles: make(map[string]string)}, nil
}

// Recover marks prior-process active runs as interrupted without replaying any
// provider work, reply, or unknown remote operation.
func (t *Tracker) Recover(ctx context.Context, profileID string) error {
	if strings.TrimSpace(profileID) == "" {
		return errors.New("profile ID is required")
	}
	return t.store.InterruptActiveRuns(ctx, profileID)
}

// Queued implements run.Observer.
func (t *Tracker) Queued(request run.Request) error {
	if err := t.store.PutRun(context.Background(), control.Run{
		ID:             request.ID,
		ProfileID:      request.ProfileID,
		ConversationID: request.ConversationID,
		EventID:        request.EventID,
		Status:         control.RunQueued,
	}); err != nil {
		return err
	}
	t.mu.Lock()
	t.profiles[request.ID] = request.ProfileID
	t.mu.Unlock()
	return nil
}

// Started implements run.Observer.
func (t *Tracker) Started(runID string) error {
	profileID, ok := t.profile(runID)
	if !ok {
		return errors.New("run profile is unavailable")
	}
	return t.store.UpdateRunStatus(context.Background(), profileID, runID, control.RunRunning, "")
}

// Finished implements run.Observer.
func (t *Tracker) Finished(result run.Result) error {
	profileID, ok := t.profile(result.RunID)
	if !ok {
		return errors.New("run profile is unavailable")
	}
	status, reason := finishedStatus(result.Status)
	if result.Status == run.StatusCanceled {
		if existing, err := t.store.RunByID(context.Background(), profileID, result.RunID); err == nil && existing.Reason == "owner cancelled" {
			reason = existing.Reason
		}
	}
	err := t.store.UpdateRunStatus(context.Background(), profileID, result.RunID, status, reason)
	if err == nil {
		t.mu.Lock()
		delete(t.profiles, result.RunID)
		t.mu.Unlock()
	}
	return err
}

func (t *Tracker) profile(runID string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	profileID, ok := t.profiles[runID]
	return profileID, ok
}

func finishedStatus(status run.Status) (control.RunStatus, string) {
	switch status {
	case run.StatusCompleted:
		return control.RunCompleted, ""
	case run.StatusCanceled:
		return control.RunCancelled, "cancelled"
	case run.StatusInterrupted:
		return control.RunInterrupted, "daemon interrupted"
	case run.StatusDeadline:
		return control.RunInterrupted, "turn deadline exceeded"
	case run.StatusGrantExpired:
		return control.RunInterrupted, "grant expired"
	case run.StatusOverflow:
		return control.RunInterrupted, "run queue overflow"
	default:
		return control.RunInterrupted, "provider turn failed"
	}
}

// Canceler is the narrow cancellation boundary retained by owner operations.
type Canceler interface {
	Cancel(runID string) bool
}

// Service exposes only the limited owner diagnostics required for run
// operations. The typed daemon dispatcher is responsible for owner transport.
type Service struct {
	profileID string
	store     *control.Store
	runs      Canceler
}

func New(profileID string, store *control.Store, runs Canceler) (*Service, error) {
	if strings.TrimSpace(profileID) == "" || store == nil || runs == nil {
		return nil, errors.New("profile ID, control store, and run canceler are required")
	}
	return &Service{profileID: profileID, store: store, runs: runs}, nil
}

// ListInput is the bounded owner run-history query.
type ListInput struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitempty"`
}

// CancelInput names a single run. Cancellation is idempotent for a terminal
// run so owner retries cannot change an already-recorded outcome.
type CancelInput struct {
	RunID string `json:"run_id"`
}

type OperationInput struct {
	OperationID string `json:"operation_id"`
}

// RunSummary omits grants, prompts, provider text, and tool-call data.
type RunSummary struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	EventID        string    `json:"event_id"`
	Status         string    `json:"status"`
	Reason         string    `json:"reason,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// OperationSummary excludes idempotency keys and input digests. Its target
// and error fields are pre-redacted control-plane summaries.
type OperationSummary struct {
	ID            string    `json:"id"`
	Scope         string    `json:"scope"`
	TargetSummary string    `json:"target_summary,omitempty"`
	Status        string    `json:"status"`
	ErrorSummary  string    `json:"error_summary,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (s *Service) List(ctx context.Context, input ListInput) (baseservice.PageResult[RunSummary], error) {
	if err := baseservice.ValidateLimit(input.Limit); err != nil {
		return baseservice.PageResult[RunSummary]{}, err
	}
	offset, err := baseservice.DecodeCursor(input.Cursor, "run.list:"+s.profileID)
	if err != nil {
		return baseservice.PageResult[RunSummary]{}, err
	}
	rows, err := s.store.ListRuns(ctx, s.profileID, offset, input.Limit+1)
	if err != nil {
		return baseservice.PageResult[RunSummary]{}, err
	}
	page := baseservice.Page[RunSummary]{Items: make([]RunSummary, 0, input.Limit)}
	for index, item := range rows {
		if index == input.Limit {
			cursor, err := baseservice.EncodeCursor("run.list:"+s.profileID, offset+input.Limit)
			if err != nil {
				return baseservice.PageResult[RunSummary]{}, fmt.Errorf("encode run cursor: %w", err)
			}
			page.NextCursor = cursor
			break
		}
		page.Items = append(page.Items, publicRun(item))
	}
	return baseservice.PageResult[RunSummary]{Data: page, Meta: s.meta(RunListMethod)}, nil
}

func (s *Service) Cancel(ctx context.Context, input CancelInput) (baseservice.Result[RunSummary], error) {
	if strings.TrimSpace(input.RunID) == "" {
		return baseservice.Result[RunSummary]{}, fmt.Errorf("%w: run ID is required", baseservice.ErrInvalidArgument)
	}
	item, err := s.store.RunByID(ctx, s.profileID, input.RunID)
	if err != nil {
		return baseservice.Result[RunSummary]{}, err
	}
	if terminal(item.Status) {
		return baseservice.Result[RunSummary]{Data: publicRun(item), Meta: s.meta(RunCancelMethod)}, nil
	}
	if !s.runs.Cancel(item.ID) {
		return baseservice.Result[RunSummary]{}, ErrRunNotActive
	}
	if err := s.store.UpdateRunStatus(ctx, s.profileID, item.ID, control.RunCancelled, "owner cancelled"); err != nil {
		return baseservice.Result[RunSummary]{}, err
	}
	item.Status = control.RunCancelled
	item.Reason = "owner cancelled"
	item.UpdatedAt = time.Now()
	return baseservice.Result[RunSummary]{Data: publicRun(item), Meta: s.meta(RunCancelMethod)}, nil
}

func (s *Service) Operation(ctx context.Context, input OperationInput) (baseservice.Result[OperationSummary], error) {
	item, err := s.operation(ctx, input)
	if err != nil {
		return baseservice.Result[OperationSummary]{}, err
	}
	return baseservice.Result[OperationSummary]{Data: publicOperation(item), Meta: s.meta(OperationGetMethod)}, nil
}

// MarkOperationUnknown explicitly prevents any future automatic retry for an
// operation that the owner cannot confidently classify.
func (s *Service) MarkOperationUnknown(ctx context.Context, input OperationInput) (baseservice.Result[OperationSummary], error) {
	item, err := s.operation(ctx, input)
	if err != nil {
		return baseservice.Result[OperationSummary]{}, err
	}
	if err := s.store.UpdateOperationStatus(ctx, item.ID, control.OperationUnknown); err != nil {
		return baseservice.Result[OperationSummary]{}, err
	}
	item.Status = control.OperationUnknown
	item.ErrorSummary = ""
	item.UpdatedAt = time.Now()
	return baseservice.Result[OperationSummary]{Data: publicOperation(item), Meta: s.meta(OperationMarkUnknownMethod)}, nil
}

func (s *Service) operation(ctx context.Context, input OperationInput) (control.Operation, error) {
	if strings.TrimSpace(input.OperationID) == "" {
		return control.Operation{}, fmt.Errorf("%w: operation ID is required", baseservice.ErrInvalidArgument)
	}
	return s.store.OperationByID(ctx, s.profileID, input.OperationID)
}

func (s *Service) meta(method string) baseservice.Meta {
	return baseservice.NewMeta(s.profileID, false, baseservice.Capability{Method: method, Scope: ownerOperationsScope, Status: "available"})
}

func terminal(status control.RunStatus) bool {
	switch status {
	case control.RunCompleted, control.RunInterrupted, control.RunCancelled:
		return true
	default:
		return false
	}
}

func publicRun(item control.Run) RunSummary {
	return RunSummary{
		ID:             item.ID,
		ConversationID: item.ConversationID,
		EventID:        item.EventID,
		Status:         string(item.Status),
		Reason:         item.Reason,
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}
}

func publicOperation(item control.Operation) OperationSummary {
	return OperationSummary{
		ID:            item.ID,
		Scope:         item.Scope,
		TargetSummary: item.TargetSummary,
		Status:        string(item.Status),
		ErrorSummary:  item.ErrorSummary,
		CreatedAt:     item.CreatedAt,
		UpdatedAt:     item.UpdatedAt,
	}
}

var _ run.Observer = (*Tracker)(nil)
