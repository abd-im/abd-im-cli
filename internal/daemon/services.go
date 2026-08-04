package daemon

import (
	"errors"

	conversationservice "github.com/abd-im/abd-im-cli/internal/service/conversation"
	groupservice "github.com/abd-im/abd-im-cli/internal/service/group"
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	profileservice "github.com/abd-im/abd-im-cli/internal/service/profile"
	socialservice "github.com/abd-im/abd-im-cli/internal/service/social"
)

// NewOwnerServices builds the fixed read surface from daemon-owned sources.
func NewOwnerServices(
	profileID string,
	profileSource profileservice.Source,
	conversationSource conversationservice.Source,
	messageSource messageservice.Source,
	groupSource groupservice.Source,
	socialSource socialservice.Source,
) (OwnerServices, error) {
	if profileSource == nil || conversationSource == nil || messageSource == nil || groupSource == nil || socialSource == nil {
		return OwnerServices{}, errors.New("all owner service sources are required")
	}
	profileReader, err := profileservice.New(profileSource, profileservice.Options{ProfileID: profileID})
	if err != nil {
		return OwnerServices{}, err
	}
	conversationReader, err := conversationservice.New(conversationSource, conversationservice.Options{ProfileID: profileID})
	if err != nil {
		return OwnerServices{}, err
	}
	messageReader, err := messageservice.New(messageSource, messageservice.Options{ProfileID: profileID})
	if err != nil {
		return OwnerServices{}, err
	}
	groupReader, err := groupservice.New(groupSource, groupservice.Options{ProfileID: profileID})
	if err != nil {
		return OwnerServices{}, err
	}
	socialReader, err := socialservice.New(socialSource, socialservice.Options{ProfileID: profileID})
	if err != nil {
		return OwnerServices{}, err
	}
	return OwnerServices{
		Profile: profileReader, Conversation: conversationReader, Message: messageReader,
		Group: groupReader, Social: socialReader,
	}, nil
}
