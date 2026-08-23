package abdim

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/google/uuid"
)

const maxTextStreamBytes = 4096

type TextStream interface {
	Append(context.Context, string) error
	Finish(context.Context) error
}

type TextStreamSender interface {
	StartTextStream(context.Context, string, string, string) (TextStream, error)
}

type TextStreamWriter struct {
	user           userContext
	globalConfig   *ccontext.GlobalConfig
	conversationID string
	clientMsgID    string

	mu         sync.Mutex
	nextIndex  int64
	totalBytes int
	finished   bool
}

func (a *Adapter) StartTextStream(ctx context.Context, initialText, recipientID, groupID string) (TextStream, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if initialText == "" || len(initialText) > maxTextStreamBytes || (strings.TrimSpace(recipientID) == "") == (strings.TrimSpace(groupID) == "") {
		return nil, errors.New("invalid text stream delivery")
	}
	user, err := a.currentUser()
	if err != nil {
		return nil, err
	}
	config := a.config
	globalConfig := &ccontext.GlobalConfig{UserID: a.userID, Token: a.token, IMConfig: &config}
	sendContext := ccontext.WithInfo(ctx, globalConfig)
	clientMsgID := uuid.NewString()
	callback := newSendCallback()
	conversationID, err := user.StartStreamMessage(
		ccontext.WithOperationID(sendContext, uuid.NewString()), callback, "text",
		initialText, clientMsgID, recipientID, groupID,
	)
	if err != nil {
		return nil, errors.New("OpenIM text stream submission failed")
	}
	select {
	case err := <-callback.done:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		return nil, errors.New("OpenIM text stream outcome is unknown")
	}
	return &TextStreamWriter{
		user: user, globalConfig: globalConfig, conversationID: conversationID,
		clientMsgID: clientMsgID, totalBytes: len(initialText),
	}, nil
}

func (w *TextStreamWriter) Append(ctx context.Context, text string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finished {
		return errors.New("text stream is already finished")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	if text == "" || w.totalBytes+len(text) > maxTextStreamBytes {
		return errors.New("invalid text stream packet")
	}
	if err := w.user.AppendStreamMessage(
		ccontext.WithOperationID(ccontext.WithInfo(ctx, w.globalConfig), uuid.NewString()),
		w.conversationID, w.clientMsgID, w.nextIndex, []string{text}, false,
	); err != nil {
		return errors.New("OpenIM text stream outcome is unknown")
	}
	w.nextIndex++
	w.totalBytes += len(text)
	return nil
}

func (w *TextStreamWriter) Finish(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finished {
		return errors.New("text stream is already finished")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := w.user.AppendStreamMessage(
		ccontext.WithOperationID(ccontext.WithInfo(ctx, w.globalConfig), uuid.NewString()),
		w.conversationID, w.clientMsgID, w.nextIndex, nil, true,
	); err != nil {
		return errors.New("OpenIM text stream outcome is unknown")
	}
	w.finished = true
	return nil
}
