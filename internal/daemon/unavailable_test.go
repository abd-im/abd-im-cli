package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/service"
	conversationservice "github.com/abd-im/abd-im-cli/internal/service/conversation"
	groupservice "github.com/abd-im/abd-im-cli/internal/service/group"
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	profileservice "github.com/abd-im/abd-im-cli/internal/service/profile"
	socialservice "github.com/abd-im/abd-im-cli/internal/service/social"
)

func TestNewOwnerServicesWithVerifiedGroupLeavesOtherSourcesClosed(t *testing.T) {
	services, err := NewOwnerServicesWithVerifiedGroup("work", verifiedGroupSource{}, groupservice.VerifiedCapabilities("sdk-test"))
	if err != nil {
		t.Fatalf("NewOwnerServicesWithVerifiedGroup() error = %v", err)
	}
	result, err := services.Group.List(context.Background(), service.OwnerAccess(service.Capability{}), groupservice.ListInput{Limit: 10})
	if err != nil || len(result.Data.Items) != 1 || result.Meta.Capability.Status != "available" || result.Meta.Capability.SDKVersion != "sdk-test" {
		t.Fatalf("verified group list = %+v, %v", result, err)
	}
	if _, err := services.Profile.Profile(context.Background(), service.OwnerAccess(service.Capability{})); !errors.Is(err, service.ErrCapabilityUnavailable) {
		t.Fatalf("unverified profile source error = %v", err)
	}
}

func TestNewOwnerServicesWithVerifiedProfileAndGroupLeavesFutureSourcesClosed(t *testing.T) {
	services, err := NewOwnerServicesWithVerifiedProfileAndGroup("work", verifiedProfileSource{}, profileservice.VerifiedCapabilities("sdk-test"), verifiedGroupSource{}, groupservice.VerifiedCapabilities("sdk-test"))
	if err != nil {
		t.Fatalf("NewOwnerServicesWithVerifiedProfileAndGroup() error = %v", err)
	}
	result, err := services.Profile.Profile(context.Background(), service.OwnerAccess(service.Capability{}))
	if err != nil || result.Data.ID != "work" || result.Meta.Capability.Status != "available" {
		t.Fatalf("verified profile = %+v, %v", result, err)
	}
	if _, err := services.Conversation.List(context.Background(), service.OwnerAccess(service.Capability{}), conversationservice.ListInput{Limit: 1}); !errors.Is(err, service.ErrCapabilityUnavailable) {
		t.Fatalf("unverified conversation source error = %v", err)
	}
}

func TestNewOwnerServicesWithVerifiedProfileConversationAndGroupLeavesFutureSourcesClosed(t *testing.T) {
	services, err := NewOwnerServicesWithVerifiedProfileConversationAndGroup(
		"work",
		verifiedProfileSource{}, profileservice.VerifiedCapabilities("sdk-test"),
		verifiedConversationSource{}, conversationservice.VerifiedCapabilities("sdk-test"),
		verifiedGroupSource{}, groupservice.VerifiedCapabilities("sdk-test"),
	)
	if err != nil {
		t.Fatalf("NewOwnerServicesWithVerifiedProfileConversationAndGroup() error = %v", err)
	}
	conversation, err := services.Conversation.List(context.Background(), service.OwnerAccess(service.Capability{}), conversationservice.ListInput{Limit: 1})
	if err != nil || len(conversation.Data.Items) != 1 || conversation.Meta.Capability.Status != "available" {
		t.Fatalf("verified conversation = %+v, %v", conversation, err)
	}
	if _, err := services.Conversation.Unread(context.Background(), service.OwnerAccess(service.Capability{})); !errors.Is(err, service.ErrCapabilityUnavailable) {
		t.Fatalf("unverified conversation unread error = %v", err)
	}
	if _, err := services.Message.History(context.Background(), service.OwnerAccess(service.Capability{}), messageservice.HistoryInput{ConversationID: "conversation-1", Limit: 1}); !errors.Is(err, service.ErrCapabilityUnavailable) {
		t.Fatalf("unverified message source error = %v", err)
	}
}

func TestNewOwnerServicesWithVerifiedProfileConversationMessageAndGroupLeavesSocialClosed(t *testing.T) {
	services, err := NewOwnerServicesWithVerifiedProfileConversationMessageAndGroup(
		"work",
		verifiedProfileSource{}, profileservice.VerifiedCapabilities("sdk-test"),
		verifiedConversationSource{}, conversationservice.VerifiedCapabilities("sdk-test"),
		verifiedMessageSource{}, messageservice.VerifiedCapabilities("sdk-test"),
		verifiedGroupSource{}, groupservice.VerifiedCapabilities("sdk-test"),
	)
	if err != nil {
		t.Fatalf("NewOwnerServicesWithVerifiedProfileConversationMessageAndGroup() error = %v", err)
	}
	messages, err := services.Message.History(context.Background(), service.OwnerAccess(service.Capability{}), messageservice.HistoryInput{ConversationID: "conversation-1", Limit: 1})
	if err != nil || len(messages.Data.Items) != 1 || messages.Meta.Capability.Status != "available" {
		t.Fatalf("verified messages = %+v, %v", messages, err)
	}
	if _, err := services.Social.Friends(context.Background(), service.OwnerAccess(service.Capability{}), socialservice.ListInput{Limit: 1}); !errors.Is(err, service.ErrCapabilityUnavailable) {
		t.Fatalf("unverified social source error = %v", err)
	}
}

func TestNewOwnerServicesWithVerifiedProfileConversationMessageGroupAndSocial(t *testing.T) {
	services, err := NewOwnerServicesWithVerifiedProfileConversationMessageGroupAndSocial(
		"work",
		verifiedProfileSource{}, profileservice.VerifiedCapabilities("sdk-test"),
		verifiedConversationSource{}, conversationservice.VerifiedCapabilities("sdk-test"),
		verifiedMessageSource{}, messageservice.VerifiedCapabilities("sdk-test"),
		verifiedGroupSource{}, groupservice.VerifiedCapabilities("sdk-test"),
		verifiedSocialSource{}, socialservice.VerifiedCapabilities("sdk-test"),
	)
	if err != nil {
		t.Fatalf("NewOwnerServicesWithVerifiedProfileConversationMessageGroupAndSocial() error = %v", err)
	}
	friends, err := services.Social.Friends(context.Background(), service.OwnerAccess(service.Capability{}), socialservice.ListInput{Limit: 1})
	if err != nil || len(friends.Data.Items) != 1 || friends.Meta.Capability.Status != "available" || friends.Meta.Capability.SDKVersion != "sdk-test" {
		t.Fatalf("verified friends = %+v, %v", friends, err)
	}
}

func TestNewOwnerServicesWithVerifiedGroupRequiresSource(t *testing.T) {
	if _, err := NewOwnerServicesWithVerifiedGroup("work", nil, nil); err == nil {
		t.Fatal("NewOwnerServicesWithVerifiedGroup() accepted nil source")
	}
}

type verifiedGroupSource struct{}

type verifiedProfileSource struct{}

type verifiedConversationSource struct{}

type verifiedMessageSource struct{}

type verifiedSocialSource struct{}

func (verifiedProfileSource) Profile(context.Context) (profileservice.Profile, error) {
	return profileservice.Profile{ID: "work"}, nil
}
func (verifiedProfileSource) Self(context.Context) (profileservice.User, error) {
	return profileservice.User{ID: "user-1"}, nil
}
func (verifiedProfileSource) Users(context.Context, []string) ([]profileservice.User, error) {
	return nil, nil
}
func (verifiedProfileSource) Daemon(context.Context) (profileservice.DaemonStatus, error) {
	return profileservice.DaemonStatus{ProfileID: "work", State: "ready", CredentialsValid: true}, nil
}
func (verifiedProfileSource) Doctor(context.Context) (profileservice.DoctorReport, error) {
	return profileservice.DoctorReport{OK: true}, nil
}

func (verifiedConversationSource) List(context.Context) ([]conversationservice.Conversation, error) {
	return []conversationservice.Conversation{{ID: "conversation-1"}}, nil
}

func (verifiedConversationSource) Get(context.Context, string) (conversationservice.Conversation, error) {
	return conversationservice.Conversation{}, nil
}

func (verifiedConversationSource) Search(context.Context, string) ([]conversationservice.Conversation, error) {
	return nil, nil
}

func (verifiedConversationSource) Unread(context.Context) (int, error) { return 0, nil }

func (verifiedMessageSource) History(context.Context, messageservice.HistoryQuery) ([]messageservice.Message, error) {
	return []messageservice.Message{{ID: "message-1", ConversationID: "conversation-1"}}, nil
}

func (verifiedMessageSource) Search(context.Context, messageservice.HistoryQuery, string) ([]messageservice.Message, error) {
	return nil, nil
}

func (verifiedMessageSource) Get(context.Context, string, string) (messageservice.Message, error) {
	return messageservice.Message{}, nil
}

func (verifiedSocialSource) Friends(context.Context) ([]socialservice.Friend, error) {
	return []socialservice.Friend{{UserID: "user-1"}}, nil
}
func (verifiedSocialSource) Friend(context.Context, string) (socialservice.Friend, error) {
	return socialservice.Friend{}, nil
}
func (verifiedSocialSource) SearchFriends(context.Context, string) ([]socialservice.Friend, error) {
	return nil, nil
}
func (verifiedSocialSource) Blacklist(context.Context) ([]socialservice.BlacklistEntry, error) {
	return nil, nil
}
func (verifiedSocialSource) Black(context.Context, string) (socialservice.BlacklistEntry, error) {
	return socialservice.BlacklistEntry{}, nil
}

func (verifiedGroupSource) List(context.Context) ([]groupservice.Group, error) {
	return []groupservice.Group{{ID: "group-1"}}, nil
}

func (verifiedGroupSource) Get(context.Context, string) (groupservice.Group, error) {
	return groupservice.Group{}, nil
}

func (verifiedGroupSource) Search(context.Context, string) ([]groupservice.Group, error) {
	return nil, nil
}

func (verifiedGroupSource) Members(context.Context, string) ([]groupservice.Member, error) {
	return nil, nil
}

func (verifiedGroupSource) SearchMembers(context.Context, string, string) ([]groupservice.Member, error) {
	return nil, nil
}
