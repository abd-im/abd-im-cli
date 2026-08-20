package daemon

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

func MessageSendMethod(profileID string, sender TextSender) (Method, error) {
	if strings.TrimSpace(profileID) == "" || sender == nil {
		return Method{}, MethodFailure(contracts.CodeInternal, "message sender is unavailable", false)
	}
	return method("message.send", func(ctx context.Context, raw json.RawMessage) (MethodResult, error) {
		var input struct {
			Text        string `json:"text"`
			RecipientID string `json:"recipient_id"`
			GroupID     string `json:"group_id"`
		}
		if err := decodeParams(raw, &input); err != nil {
			return MethodResult{}, err
		}
		input.Text = strings.TrimSpace(input.Text)
		if input.Text == "" || len(input.Text) > 4096 || (input.RecipientID == "") == (input.GroupID == "") {
			return MethodResult{}, MethodFailure(contracts.CodeInvalidArgument, "text and exactly one message target are required", false)
		}
		if err := sender.SendText(ctx, input.Text, input.RecipientID, input.GroupID); err != nil {
			return MethodResult{}, MethodFailure(contracts.CodeSDKError, "send message failed", false)
		}
		return MethodResult{Data: map[string]bool{"sent": true}, Meta: contracts.Meta{ProfileID: profileID}}, nil
	}), nil
}
