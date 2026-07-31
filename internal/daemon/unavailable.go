package daemon

import (
	"context"
	"errors"

	conversationservice "github.com/abd-im/abd-im-cli/internal/service/conversation"
	groupservice "github.com/abd-im/abd-im-cli/internal/service/group"
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	profileservice "github.com/abd-im/abd-im-cli/internal/service/profile"
	socialservice "github.com/abd-im/abd-im-cli/internal/service/social"
)

var errUnverifiedSource = errors.New("typed source is not verified")

// NewUnverifiedOwnerServices makes the fixed owner registry available without
// granting access to any source whose SDK/server mapping lacks an integration
// gate. Service capabilities remain not_validated and reject requests first.
func NewUnverifiedOwnerServices(profileID string) (OwnerServices, error) {
	profileReader, err := profileservice.New(unverifiedProfileSource{}, profileservice.Options{ProfileID: profileID})
	if err != nil {
		return OwnerServices{}, err
	}
	conversationReader, err := conversationservice.New(unverifiedConversationSource{}, conversationservice.Options{ProfileID: profileID})
	if err != nil {
		return OwnerServices{}, err
	}
	messageReader, err := messageservice.New(unverifiedMessageSource{}, messageservice.Options{ProfileID: profileID})
	if err != nil {
		return OwnerServices{}, err
	}
	groupReader, err := groupservice.New(unverifiedGroupSource{}, groupservice.Options{ProfileID: profileID})
	if err != nil {
		return OwnerServices{}, err
	}
	socialReader, err := socialservice.New(unverifiedSocialSource{}, socialservice.Options{ProfileID: profileID})
	if err != nil {
		return OwnerServices{}, err
	}
	return OwnerServices{Profile: profileReader, Conversation: conversationReader, Message: messageReader, Group: groupReader, Social: socialReader}, nil
}

func (unverifiedProfileSource) Profile(context.Context) (profileservice.Profile, error) { return profileservice.Profile{}, errUnverifiedSource }
func (unverifiedProfileSource) Self(context.Context) (profileservice.User, error)       { return profileservice.User{}, errUnverifiedSource }
func (unverifiedProfileSource) Users(context.Context, []string) ([]profileservice.User, error) {
	return nil, errUnverifiedSource
}
func (unverifiedProfileSource) Daemon(context.Context) (profileservice.DaemonStatus, error) {
	return profileservice.DaemonStatus{}, errUnverifiedSource
}
func (unverifiedProfileSource) Doctor(context.Context) (profileservice.DoctorReport, error) {
	return profileservice.DoctorReport{}, errUnverifiedSource
}

type unverifiedProfileSource struct{}

type unverifiedConversationSource struct{}

func (unverifiedConversationSource) List(context.Context) ([]conversationservice.Conversation, error) {
	return nil, errUnverifiedSource
}
func (unverifiedConversationSource) Get(context.Context, string) (conversationservice.Conversation, error) {
	return conversationservice.Conversation{}, errUnverifiedSource
}
func (unverifiedConversationSource) Search(context.Context, string) ([]conversationservice.Conversation, error) {
	return nil, errUnverifiedSource
}
func (unverifiedConversationSource) Unread(context.Context) (int, error) { return 0, errUnverifiedSource }

type unverifiedMessageSource struct{}

func (unverifiedMessageSource) History(context.Context, messageservice.HistoryQuery) ([]messageservice.Message, error) {
	return nil, errUnverifiedSource
}
func (unverifiedMessageSource) Search(context.Context, messageservice.HistoryQuery, string) ([]messageservice.Message, error) {
	return nil, errUnverifiedSource
}
func (unverifiedMessageSource) Get(context.Context, string, string) (messageservice.Message, error) {
	return messageservice.Message{}, errUnverifiedSource
}

type unverifiedGroupSource struct{}

func (unverifiedGroupSource) List(context.Context) ([]groupservice.Group, error) { return nil, errUnverifiedSource }
func (unverifiedGroupSource) Get(context.Context, string) (groupservice.Group, error) {
	return groupservice.Group{}, errUnverifiedSource
}
func (unverifiedGroupSource) Search(context.Context, string) ([]groupservice.Group, error) {
	return nil, errUnverifiedSource
}
func (unverifiedGroupSource) Members(context.Context, string) ([]groupservice.Member, error) {
	return nil, errUnverifiedSource
}
func (unverifiedGroupSource) SearchMembers(context.Context, string, string) ([]groupservice.Member, error) {
	return nil, errUnverifiedSource
}

type unverifiedSocialSource struct{}

func (unverifiedSocialSource) Friends(context.Context) ([]socialservice.Friend, error) {
	return nil, errUnverifiedSource
}
func (unverifiedSocialSource) Friend(context.Context, string) (socialservice.Friend, error) {
	return socialservice.Friend{}, errUnverifiedSource
}
func (unverifiedSocialSource) SearchFriends(context.Context, string) ([]socialservice.Friend, error) {
	return nil, errUnverifiedSource
}
func (unverifiedSocialSource) Blacklist(context.Context) ([]socialservice.BlacklistEntry, error) {
	return nil, errUnverifiedSource
}
func (unverifiedSocialSource) Black(context.Context, string) (socialservice.BlacklistEntry, error) {
	return socialservice.BlacklistEntry{}, errUnverifiedSource
}
