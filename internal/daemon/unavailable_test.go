package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/service"
	conversationservice "github.com/abd-im/abd-im-cli/internal/service/conversation"
	groupservice "github.com/abd-im/abd-im-cli/internal/service/group"
	profileservice "github.com/abd-im/abd-im-cli/internal/service/profile"
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

func TestNewOwnerServicesWithVerifiedGroupRequiresSource(t *testing.T) {
	if _, err := NewOwnerServicesWithVerifiedGroup("work", nil, nil); err == nil {
		t.Fatal("NewOwnerServicesWithVerifiedGroup() accepted nil source")
	}
}

type verifiedGroupSource struct{}

type verifiedProfileSource struct{}

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
