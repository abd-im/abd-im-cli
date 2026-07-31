package connector

import (
	"context"
	"errors"
	"testing"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/profile"
	profileservice "github.com/abd-im/abd-im-cli/internal/service/profile"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

type credentialStore struct {
	token []byte
	ref   string
}

func (s credentialStore) Put(context.Context, string, []byte) (string, error) { return s.ref, nil }
func (s credentialStore) Get(_ context.Context, ref string) ([]byte, error) {
	if ref != s.ref {
		return nil, errors.New("unexpected reference")
	}
	return append([]byte(nil), s.token...), nil
}

func validConfig() Config {
	return Config{
		ProfileID:     "work",
		UserID:        "user-1",
		CredentialRef: "keyring:work",
		Credentials:   credentialStore{ref: "keyring:work", token: []byte("secret")},
		SDKConfig: sdk_struct.IMConfig{
			ApiAddr:     "https://im.example.test",
			WsAddr:      "wss://im.example.test/ws",
			PlatformID:  7,
			DataDir:     "/tmp/abdim/work/sdk",
			LogFilePath: "/tmp/abdim/work/sdk.log",
		},
	}
}

func TestPrepareResolvesCredentialWithoutStartingSDK(t *testing.T) {
	prepared, err := Prepare(context.Background(), validConfig())
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if prepared == nil || prepared.Adapter == nil || prepared.SDKFactory() == nil {
		t.Fatal("Prepare() returned incomplete daemon composition")
	}
	if source, err := prepared.GroupSource(); err != nil || source == nil {
		t.Fatalf("GroupSource() = %v, %v", source, err)
	}
	if source, err := prepared.ConversationSource(); err != nil || source == nil {
		t.Fatalf("ConversationSource() = %v, %v", source, err)
	}
	if source, err := prepared.MessageSource(); err != nil || source == nil {
		t.Fatalf("MessageSource() = %v, %v", source, err)
	}
	if source, err := prepared.ProfileSource(func() profileservice.DaemonStatus { return profileservice.DaemonStatus{State: "ready"} }); err != nil || source == nil {
		t.Fatalf("ProfileSource() = %v, %v", source, err)
	}
	if got := prepared.Adapter.Context(); got == nil {
		t.Fatal("adapter context is nil")
	}
}

func TestPrepareRejectsMissingDeploymentInputs(t *testing.T) {
	checks := []Config{
		{},
		func() Config { c := validConfig(); c.Credentials = nil; return c }(),
		func() Config { c := validConfig(); c.CredentialRef = ""; return c }(),
		func() Config { c := validConfig(); c.SDKConfig.ApiAddr = ""; return c }(),
	}
	for index, config := range checks {
		if _, err := Prepare(context.Background(), config); err == nil {
			t.Errorf("case %d: Prepare() error = nil", index)
		}
	}
}

func TestPreparedFactoryReturnsProjectSDKContract(t *testing.T) {
	prepared, err := Prepare(context.Background(), validConfig())
	if err != nil {
		t.Fatal(err)
	}
	var _ contracts.SDK = prepared.SDKFactory()()
}

var _ profile.CredentialStore = credentialStore{}
