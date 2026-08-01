// Package connector defines the deployment-owned boundary that supplies
// authenticated SDK settings to one abdim daemon profile.
package connector

import (
	"context"
	"errors"
	"strings"

	"github.com/abd-im/abd-im-cli/internal/bridge"
	"github.com/abd-im/abd-im-cli/internal/bridge/abdim"
	conversationcapability "github.com/abd-im/abd-im-cli/internal/capability/conversation"
	groupcapability "github.com/abd-im/abd-im-cli/internal/capability/group"
	messagecapability "github.com/abd-im/abd-im-cli/internal/capability/message"
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

// Config contains deployment-owned identity and server settings. CredentialRef
// is opaque; the token is resolved only while preparing the daemon adapter.
type Config struct {
	ProfileID     string
	UserID        string
	CredentialRef string
	SDKConfig     sdk_struct.IMConfig
	Credentials   profile.CredentialStore
}

// Prepared is the daemon-owned SDK composition. The adapter is created once
// and returned as a factory so bridge.LoginMgr remains the lifecycle owner.
type Prepared struct {
	Adapter       *abdim.Adapter
	profileID     string
	userID        string
	credentialRef string
}

// Prepare resolves the configured credential and constructs the SDK adapter.
// It does not initialize the SDK, open its data directory, or start network
// activity. The caller must pass the returned Adapter to daemon.Runtime.
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
		ProfileID: config.ProfileID,
		UserID:    config.UserID,
		Token:     token,
		SDKConfig: config.SDKConfig,
	})
	for index := range token {
		token[index] = 0
	}
	if err != nil {
		return nil, err
	}
	return &Prepared{Adapter: adapter, profileID: config.ProfileID, userID: config.UserID, credentialRef: config.CredentialRef}, nil
}

// SDKFactory adapts the prepared daemon-owned adapter to bridge.LoginMgr.
func (p *Prepared) SDKFactory() bridge.SDKFactory {
	if p == nil || p.Adapter == nil {
		return nil
	}
	return func() contracts.SDK { return p.Adapter }
}

// GroupSource exposes the verified server-read group facade using the
// daemon-owned adapter context. It does not expose the SDK or its data store.
func (p *Prepared) GroupSource() (*groupservice.SDKSource, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return groupservice.NewSDKSource(groupservice.OpenIMClient{Context: p.Adapter.Context})
}

// GroupCreator exposes the verified server action through the daemon-owned
// adapter context. It does not expose SDK local synchronization APIs.
func (p *Prepared) GroupCreator() (*groupcapability.OpenIMCreator, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return groupcapability.NewOpenIMCreator(groupcapability.OpenIMCreator{Context: p.Adapter.Context})
}

// MessageSender exposes the daemon-owned adapter only through the narrow
// message action interface. It does not expose the SDK or its local store.
func (p *Prepared) MessageSender() (messagecapability.Sender, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return p.Adapter, nil
}

// MessageAtSender exposes only the daemon-owned text-at sender to the
// message capability. It does not expose a general SDK conversation API.
func (p *Prepared) MessageAtSender() (messagecapability.AtSender, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return p.Adapter, nil
}

// MessageQuoteSource exposes the fixed server history and narrow quote sender
// as one daemon-owned action source. It has no local SDK database API.
func (p *Prepared) MessageQuoteSource() (*messagecapability.OpenIMQuoteSource, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return messagecapability.NewOpenIMQuoteSource(messageservice.OpenIMClient{Context: p.Adapter.Context}, p.Adapter, p.userID)
}

// ConversationMarkRead exposes the fixed server message-boundary read and
// mark-conversation-as-read action without exposing SDK local state.
func (p *Prepared) ConversationMarkRead() (*conversationcapability.OpenIMMarkRead, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return conversationcapability.NewOpenIMMarkRead(conversationcapability.OpenIMMarkRead{
		Context: p.Adapter.Context,
		Client:  messageservice.OpenIMClient{Context: p.Adapter.Context},
	})
}

// ConversationSource exposes the verified server-read conversation facade
// using the daemon-owned adapter context. It does not expose SDK local data.
func (p *Prepared) ConversationSource() (*conversationservice.SDKSource, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return conversationservice.NewSDKSource(conversationservice.OpenIMClient{Context: p.Adapter.Context})
}

// MessageSource exposes the verified server-read message facade using the
// daemon-owned adapter context. It does not expose SDK local data.
func (p *Prepared) MessageSource() (*messageservice.SDKSource, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return messageservice.NewSDKSource(messageservice.OpenIMClient{Context: p.Adapter.Context})
}

// SocialSource exposes the verified server-read friend and blacklist facade
// using the daemon-owned adapter context. It does not expose SDK local data.
func (p *Prepared) SocialSource() (*socialservice.SDKSource, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return socialservice.NewSDKSource(socialservice.OpenIMClient{Context: p.Adapter.Context})
}

// ProfileSource exposes fixed local profile/runtime facts and the verified
// OpenIM user server read. It never calls an SDK cache or database API.
func (p *Prepared) ProfileSource(daemonStatus func() profileservice.DaemonStatus) (*profileservice.OpenIMSource, error) {
	if p == nil || p.Adapter == nil {
		return nil, errors.New("prepared daemon adapter is required")
	}
	return profileservice.NewOpenIMSource(profileservice.OpenIMSourceConfig{
		Profile: profileservice.Profile{
			ID:            p.profileID,
			Name:          p.profileID,
			CredentialRef: p.credentialRef,
			SDKVersion:    open_im_sdk.GetSdkVersion(),
		},
		SelfID: p.userID,
		Client: profileservice.OpenIMClient{Context: p.Adapter.Context},
		Daemon: daemonStatus,
	})
}

func validateConfig(config Config) error {
	if strings.TrimSpace(config.ProfileID) == "" || strings.TrimSpace(config.UserID) == "" {
		return errors.New("profile ID and user ID are required")
	}
	if strings.TrimSpace(config.CredentialRef) == "" {
		return errors.New("credential reference is required")
	}
	if config.Credentials == nil {
		return errors.New("credential store is required")
	}
	if strings.TrimSpace(config.SDKConfig.ApiAddr) == "" || strings.TrimSpace(config.SDKConfig.WsAddr) == "" || config.SDKConfig.PlatformID <= 0 || strings.TrimSpace(config.SDKConfig.DataDir) == "" || strings.TrimSpace(config.SDKConfig.LogFilePath) == "" {
		return errors.New("SDK API, WebSocket, platform, data, and log configuration are required")
	}
	return nil
}
