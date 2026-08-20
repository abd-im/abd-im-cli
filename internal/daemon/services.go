package daemon

import (
	"errors"

	conversationservice "github.com/abd-im/abd-im-cli/internal/service/conversation"
	groupservice "github.com/abd-im/abd-im-cli/internal/service/group"
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	profileservice "github.com/abd-im/abd-im-cli/internal/service/profile"
	socialservice "github.com/abd-im/abd-im-cli/internal/service/social"
)

// NewServices builds the fixed CLI surface from one SDK identity.
func NewServices(
	profileID string,
	profileSource profileservice.Source,
	conversationSource conversationservice.Source,
	messageSource messageservice.Source,
	groupSource groupservice.Source,
	socialSource socialservice.Source,
) (Services, error) {
	if profileSource == nil || conversationSource == nil || messageSource == nil || groupSource == nil || socialSource == nil {
		return Services{}, errors.New("all service sources are required")
	}
	profileReader, err := profileservice.New(profileSource, profileservice.Options{ProfileID: profileID})
	if err != nil {
		return Services{}, err
	}
	conversationReader, err := conversationservice.New(conversationSource, conversationservice.Options{ProfileID: profileID})
	if err != nil {
		return Services{}, err
	}
	messageReader, err := messageservice.New(messageSource, messageservice.Options{ProfileID: profileID})
	if err != nil {
		return Services{}, err
	}
	groupReader, err := groupservice.New(groupSource, groupservice.Options{ProfileID: profileID})
	if err != nil {
		return Services{}, err
	}
	socialReader, err := socialservice.New(socialSource, socialservice.Options{ProfileID: profileID})
	if err != nil {
		return Services{}, err
	}
	return Services{
		Profile: profileReader, Conversation: conversationReader, Message: messageReader,
		Group: groupReader, Social: socialReader,
	}, nil
}
