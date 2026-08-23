package group

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

func ConversationKindFromGroupEx(ex string) contracts.ConversationKind {
	var value struct {
		ABD struct {
			Kind    string `json:"kind"`
			Version int    `json:"version"`
		} `json:"abd"`
	}
	if json.Unmarshal([]byte(ex), &value) != nil || value.ABD.Kind != string(contracts.ConversationKindAgentWorkspace) || value.ABD.Version != 1 {
		return contracts.ConversationKindChat
	}
	return contracts.ConversationKindAgentWorkspace
}

func (s *SDKSource) ConversationKind(ctx context.Context, groupID string) (contracts.ConversationKind, error) {
	if strings.TrimSpace(groupID) == "" {
		return contracts.ConversationKindChat, errors.New("group ID is required")
	}
	items, err := s.client.Groups(ctx, []string{groupID})
	if err != nil {
		return contracts.ConversationKindChat, err
	}
	for _, item := range items {
		if item != nil && item.GroupID == groupID {
			return ConversationKindFromGroupEx(item.Ex), nil
		}
	}
	return contracts.ConversationKindChat, nil
}
