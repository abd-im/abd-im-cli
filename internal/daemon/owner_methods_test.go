package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/service"
	conversationservice "github.com/abd-im/abd-im-cli/internal/service/conversation"
	groupservice "github.com/abd-im/abd-im-cli/internal/service/group"
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	profileservice "github.com/abd-im/abd-im-cli/internal/service/profile"
	socialservice "github.com/abd-im/abd-im-cli/internal/service/social"
)

func TestOwnerMethodsRegisterAndCallAllTypedReads(t *testing.T) {
	methods, err := OwnerMethods(newOwnerServices(t, ownerGroupSource{}))
	if err != nil {
		t.Fatalf("OwnerMethods() error = %v", err)
	}
	if len(methods) != 22 {
		t.Fatalf("OwnerMethods() count = %d, want 22", len(methods))
	}
	dispatcher, err := NewDispatcher("work", methods)
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}

	profile, err := dispatcher.Handle(context.Background(), ownerRequest(profileservice.ProfileGet, `{}`))
	if err != nil {
		t.Fatalf("profile.get error = %v", err)
	}
	var profileData profileservice.Profile
	if err := json.Unmarshal(profile.Data, &profileData); err != nil {
		t.Fatalf("decode profile.get data: %v", err)
	}
	if !profile.OK || profileData.ID != "work" || profileData.Name != "Work" || profile.Meta == nil || profile.Meta.Schema != service.SchemaVersion || profile.Meta.Capability == nil || profile.Meta.Capability.Method != profileservice.ProfileGet {
		t.Fatalf("profile.get response = %+v", profile)
	}

	group, err := dispatcher.Handle(context.Background(), ownerRequest(groupservice.GetMethod, `{"group_id":"group-1"}`))
	if err != nil {
		t.Fatalf("group.get error = %v", err)
	}
	var groupData groupservice.Group
	if err := json.Unmarshal(group.Data, &groupData); err != nil {
		t.Fatalf("decode group.get data: %v", err)
	}
	if !group.OK || groupData.ID != "group-1" || groupData.Name != "Group" || group.Meta == nil || group.Meta.Capability == nil || group.Meta.Capability.Method != groupservice.GetMethod {
		t.Fatalf("group.get response = %+v", group)
	}

	invalid, err := dispatcher.Handle(context.Background(), ownerRequest(groupservice.GetMethod, `{}`))
	if err != nil {
		t.Fatalf("invalid group.get error = %v", err)
	}
	if invalid.OK || invalid.Error == nil || invalid.Error.Code != contracts.CodeInvalidArgument || invalid.Error.Message != "invalid typed service parameters" {
		t.Fatalf("invalid group.get response = %+v", invalid)
	}
}

func TestOwnerMethodsRedactServiceErrors(t *testing.T) {
	const secret = "service-error-token-marker"
	methods, err := OwnerMethods(newOwnerServices(t, ownerGroupSource{getErr: errors.New(secret)}))
	if err != nil {
		t.Fatalf("OwnerMethods() error = %v", err)
	}
	dispatcher, err := NewDispatcher("work", methods)
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}
	response, err := dispatcher.Handle(context.Background(), ownerRequest(groupservice.GetMethod, `{"group_id":"group-1"}`))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	payload, marshalErr := json.Marshal(response)
	if marshalErr != nil || response.OK || response.Error == nil || response.Error.Code != contracts.CodeInternal || strings.Contains(string(payload), secret) {
		t.Fatalf("unsafe response = %s, marshal error = %v", payload, marshalErr)
	}
}

func TestOwnerMethodsRequireEveryTypedService(t *testing.T) {
	if _, err := OwnerMethods(OwnerServices{}); err == nil {
		t.Fatal("OwnerMethods() accepted missing services")
	}
}

func newOwnerServices(t *testing.T, groups groupservice.Source) OwnerServices {
	t.Helper()
	profiles, err := profileservice.New(ownerProfileSource{}, profileservice.Options{
		ProfileID:    "work",
		Capabilities: profileCapabilities(),
	})
	if err != nil {
		t.Fatalf("profile.New() error = %v", err)
	}
	conversations, err := conversationservice.New(ownerConversationSource{}, conversationservice.Options{
		ProfileID:    "work",
		Capabilities: readCapabilities([]string{conversationservice.ListMethod, conversationservice.GetMethod, conversationservice.SearchMethod, conversationservice.UnreadMethod}, conversationservice.ReadScope),
	})
	if err != nil {
		t.Fatalf("conversation.New() error = %v", err)
	}
	messages, err := messageservice.New(ownerMessageSource{}, messageservice.Options{
		ProfileID:    "work",
		Capabilities: readCapabilities([]string{messageservice.HistoryMethod, messageservice.SearchMethod, messageservice.GetMethod}, messageservice.ReadScope),
	})
	if err != nil {
		t.Fatalf("message.New() error = %v", err)
	}
	groupReader, err := groupservice.New(groups, groupservice.Options{
		ProfileID:    "work",
		Capabilities: readCapabilities([]string{groupservice.ListMethod, groupservice.GetMethod, groupservice.SearchMethod, groupservice.MembersListMethod, groupservice.MembersSearchMethod}, groupservice.ReadScope),
	})
	if err != nil {
		t.Fatalf("group.New() error = %v", err)
	}
	social, err := socialservice.New(ownerSocialSource{}, socialservice.Options{
		ProfileID: "work",
		Capabilities: map[string]service.Capability{
			socialservice.FriendListMethod:   {Method: socialservice.FriendListMethod, Scope: socialservice.FriendReadScope, Status: "available"},
			socialservice.FriendGetMethod:    {Method: socialservice.FriendGetMethod, Scope: socialservice.FriendReadScope, Status: "available"},
			socialservice.FriendSearchMethod: {Method: socialservice.FriendSearchMethod, Scope: socialservice.FriendReadScope, Status: "available"},
			socialservice.BlackListMethod:    {Method: socialservice.BlackListMethod, Scope: socialservice.BlackReadScope, Status: "available"},
			socialservice.BlackGetMethod:     {Method: socialservice.BlackGetMethod, Scope: socialservice.BlackReadScope, Status: "available"},
		},
	})
	if err != nil {
		t.Fatalf("social.New() error = %v", err)
	}
	return OwnerServices{Profile: profiles, Conversation: conversations, Message: messages, Group: groupReader, Social: social}
}

func profileCapabilities() map[string]service.Capability {
	methods := []string{profileservice.ProfileGet, profileservice.UserMe, profileservice.UserGet, profileservice.DaemonGet, profileservice.DoctorGet}
	capabilities := make(map[string]service.Capability, len(methods))
	for _, method := range methods {
		capabilities[method] = service.Capability{Method: method, Scope: method + ".read", Status: "available"}
	}
	return capabilities
}

func readCapabilities(methods []string, scope string) map[string]service.Capability {
	capabilities := make(map[string]service.Capability, len(methods))
	for _, method := range methods {
		capabilities[method] = service.Capability{Method: method, Scope: scope, Status: "available"}
	}
	return capabilities
}

type ownerProfileSource struct{}

func (ownerProfileSource) Profile(context.Context) (profileservice.Profile, error) {
	return profileservice.Profile{ID: "work", Name: "Work"}, nil
}
func (ownerProfileSource) Self(context.Context) (profileservice.User, error) {
	return profileservice.User{ID: "user-1"}, nil
}
func (ownerProfileSource) Users(context.Context, []string) ([]profileservice.User, error) {
	return []profileservice.User{{ID: "user-1"}}, nil
}
func (ownerProfileSource) Daemon(context.Context) (profileservice.DaemonStatus, error) {
	return profileservice.DaemonStatus{ProfileID: "work", State: "ready"}, nil
}
func (ownerProfileSource) Doctor(context.Context) (profileservice.DoctorReport, error) {
	return profileservice.DoctorReport{OK: true}, nil
}

type ownerConversationSource struct{}

func (ownerConversationSource) List(context.Context) ([]conversationservice.Conversation, error) {
	return []conversationservice.Conversation{{ID: "conversation-1"}}, nil
}
func (ownerConversationSource) Get(context.Context, string) (conversationservice.Conversation, error) {
	return conversationservice.Conversation{ID: "conversation-1"}, nil
}
func (ownerConversationSource) Search(context.Context, string) ([]conversationservice.Conversation, error) {
	return []conversationservice.Conversation{{ID: "conversation-1"}}, nil
}
func (ownerConversationSource) Unread(context.Context) (int, error) { return 0, nil }

type ownerMessageSource struct{}

func (ownerMessageSource) History(context.Context, messageservice.HistoryQuery) ([]messageservice.Message, error) {
	return []messageservice.Message{{ID: "message-1", ConversationID: "conversation-1"}}, nil
}
func (ownerMessageSource) Search(context.Context, messageservice.HistoryQuery, string) ([]messageservice.Message, error) {
	return []messageservice.Message{{ID: "message-1", ConversationID: "conversation-1"}}, nil
}
func (ownerMessageSource) Get(context.Context, string, string) (messageservice.Message, error) {
	return messageservice.Message{ID: "message-1", ConversationID: "conversation-1"}, nil
}

type ownerGroupSource struct{ getErr error }

func (ownerGroupSource) List(context.Context) ([]groupservice.Group, error) {
	return []groupservice.Group{{ID: "group-1", Name: "Group"}}, nil
}
func (source ownerGroupSource) Get(context.Context, string) (groupservice.Group, error) {
	if source.getErr != nil {
		return groupservice.Group{}, source.getErr
	}
	return groupservice.Group{ID: "group-1", Name: "Group"}, nil
}
func (ownerGroupSource) Search(context.Context, string) ([]groupservice.Group, error) {
	return []groupservice.Group{{ID: "group-1", Name: "Group"}}, nil
}
func (ownerGroupSource) Members(context.Context, string) ([]groupservice.Member, error) {
	return []groupservice.Member{{GroupID: "group-1", UserID: "user-1"}}, nil
}
func (ownerGroupSource) SearchMembers(context.Context, string, string) ([]groupservice.Member, error) {
	return []groupservice.Member{{GroupID: "group-1", UserID: "user-1"}}, nil
}

type ownerSocialSource struct{}

func (ownerSocialSource) Friends(context.Context) ([]socialservice.Friend, error) {
	return []socialservice.Friend{{UserID: "user-1"}}, nil
}
func (ownerSocialSource) Friend(context.Context, string) (socialservice.Friend, error) {
	return socialservice.Friend{UserID: "user-1"}, nil
}
func (ownerSocialSource) SearchFriends(context.Context, string) ([]socialservice.Friend, error) {
	return []socialservice.Friend{{UserID: "user-1"}}, nil
}
func (ownerSocialSource) Blacklist(context.Context) ([]socialservice.BlacklistEntry, error) {
	return []socialservice.BlacklistEntry{{UserID: "user-1"}}, nil
}
func (ownerSocialSource) Black(context.Context, string) (socialservice.BlacklistEntry, error) {
	return socialservice.BlacklistEntry{UserID: "user-1"}, nil
}
