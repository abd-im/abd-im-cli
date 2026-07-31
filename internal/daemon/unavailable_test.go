package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/service"
	groupservice "github.com/abd-im/abd-im-cli/internal/service/group"
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

func TestNewOwnerServicesWithVerifiedGroupRequiresSource(t *testing.T) {
	if _, err := NewOwnerServicesWithVerifiedGroup("work", nil, nil); err == nil {
		t.Fatal("NewOwnerServicesWithVerifiedGroup() accepted nil source")
	}
}

type verifiedGroupSource struct{}

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
