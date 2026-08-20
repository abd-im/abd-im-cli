// Package connector prepares one authenticated local SDK identity.
package connector

import (
	"context"
	"errors"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/bridge"
	"github.com/abd-im/abd-im-cli/internal/bridge/abdim"
	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/profile"
	conversationservice "github.com/abd-im/abd-im-cli/internal/service/conversation"
	groupservice "github.com/abd-im/abd-im-cli/internal/service/group"
	messageservice "github.com/abd-im/abd-im-cli/internal/service/message"
	profileservice "github.com/abd-im/abd-im-cli/internal/service/profile"
	socialservice "github.com/abd-im/abd-im-cli/internal/service/social"
	"github.com/abd-im/abd-im-sdk-core/v3/open_im_sdk"
	"github.com/abd-im/abd-im-sdk-core/v3/sdk_struct"
)

type Config struct {
	ProfileID     string
	UserID        string
	CredentialRef string
	SDKConfig     sdk_struct.IMConfig
	Credentials   profile.CredentialStore
}

type Prepared struct {
	Adapter       *abdim.Adapter
	profileID     string
	userID        string
	credentialRef string
}

func Prepare(ctx context.Context, config Config) (*Prepared, error) {
	if ctx == nil {
		return nil, errors.New("connector context is required")
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	token, err := config.Credentials.Get(ctx, config.CredentialRef)
	if err != nil {
		return nil, errors.New("resolve profile credential")
	}
	if len(token) == 0 {
		return nil, errors.New("resolved profile credential is empty")
	}
	adapter, err := abdim.New(abdim.Config{
		ProfileID: config.ProfileID, UserID: config.UserID, Token: token, SDKConfig: config.SDKConfig,
	})
	for index := range token {
		token[index] = 0
	}
	if err != nil {
		return nil, err
	}
	return &Prepared{Adapter: adapter, profileID: config.ProfileID, userID: config.UserID, credentialRef: config.CredentialRef}, nil
}

func (p *Prepared) SDKFactory() bridge.SDKFactory {
	if p == nil || p.Adapter == nil {
		return nil
	}
	return func() contracts.SDK { return p.Adapter }
}

func (p *Prepared) GroupSource() (*groupservice.SDKSource, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	return groupservice.NewSDKSource(groupservice.OpenIMClient{Context: p.Adapter.Context})
}

func (p *Prepared) ConversationSource() (*conversationservice.SDKSource, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	return conversationservice.NewSDKSource(conversationservice.OpenIMClient{Context: p.Adapter.Context})
}

func (p *Prepared) MessageSource() (*messageservice.SDKSource, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	return messageservice.NewSDKSource(messageservice.OpenIMClient{Context: p.Adapter.Context})
}

func (p *Prepared) SocialSource() (*socialservice.SDKSource, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	return socialservice.NewSDKSource(socialservice.OpenIMClient{Context: p.Adapter.Context})
}

func (p *Prepared) ProfileSource(daemonStatus func() profileservice.DaemonStatus) (*profileservice.OpenIMSource, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	return profileservice.NewOpenIMSource(profileservice.OpenIMSourceConfig{
		Profile: profileservice.Profile{ID: p.profileID, Name: p.profileID, CredentialRef: p.credentialRef, SDKVersion: open_im_sdk.GetSdkVersion()},
		SelfID:  p.userID, Client: profileservice.OpenIMClient{Context: p.Adapter.Context}, Daemon: daemonStatus,
	})
}

func (p *Prepared) validate() error {
	if p == nil || p.Adapter == nil {
		return errors.New("prepared daemon adapter is required")
	}
	return nil
}

func validateConfig(config Config) error {
	if strings.TrimSpace(config.ProfileID) == "" || strings.TrimSpace(config.UserID) == "" || strings.TrimSpace(config.CredentialRef) == "" {
		return errors.New("profile ID, user ID, and credential reference are required")
	}
	if config.Credentials == nil {
		return errors.New("credential store is required")
	}
	if strings.TrimSpace(config.SDKConfig.ApiAddr) == "" || strings.TrimSpace(config.SDKConfig.WsAddr) == "" || config.SDKConfig.PlatformID <= 0 || strings.TrimSpace(config.SDKConfig.DataDir) == "" || strings.TrimSpace(config.SDKConfig.LogFilePath) == "" {
		return errors.New("SDK API, WebSocket, platform, data, and log configuration are required")
	}
	return nil
}
