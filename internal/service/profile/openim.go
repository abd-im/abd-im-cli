package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/abd-im/abd-im-protocol/sdkws"
	pbuser "github.com/abd-im/abd-im-protocol/user"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/google/uuid"
)

var ErrUserNotFound = errors.New("user not found")

// Client is the narrow authenticated server-read surface used by OpenIMSource.
// It has no SDK database methods, so profile and user reads cannot reach local
// SDK tables.
type Client interface {
	Users(context.Context, []string) ([]User, error)
}

// OpenIMClient invokes the fixed OpenIM user endpoint with the daemon-owned
// SDK context. It avoids the SDK API helper because that helper can log tokens.
type OpenIMClient struct {
	Context    func() context.Context
	HTTPClient *http.Client
}

func (c OpenIMClient) Users(ctx context.Context, ids []string) ([]User, error) {
	if len(ids) == 0 {
		return nil, errors.New("OpenIM user IDs are required")
	}
	request, config, done, err := c.requestContext(ctx)
	if err != nil {
		return nil, err
	}
	defer done()

	var response pbuser.GetDesignateUsersResp
	if err := c.invoke(request, config, "/user/get_users_info", &pbuser.GetDesignateUsersReq{UserIDs: append([]string(nil), ids...)}, &response); err != nil {
		return nil, err
	}
	return usersFromSDK(response.UsersInfo), nil
}

func (c OpenIMClient) requestContext(caller context.Context) (context.Context, *ccontext.GlobalConfig, func(), error) {
	if c.Context == nil {
		return nil, nil, nil, errors.New("OpenIM SDK context is required")
	}
	if caller == nil {
		return nil, nil, nil, errors.New("caller context is required")
	}
	if err := caller.Err(); err != nil {
		return nil, nil, nil, err
	}
	base := c.Context()
	if base == nil {
		return nil, nil, nil, errors.New("OpenIM SDK context is nil")
	}
	config, ok := base.Value(ccontext.GlobalConfigKey{}).(*ccontext.GlobalConfig)
	if !ok || config == nil || strings.TrimSpace(config.UserID) == "" || strings.TrimSpace(config.Token) == "" || strings.TrimSpace(config.ApiAddr) == "" {
		return nil, nil, nil, errors.New("OpenIM SDK context is not authenticated")
	}
	request, cancel := context.WithCancel(base)
	stop := context.AfterFunc(caller, cancel)
	return ccontext.WithOperationID(request, uuid.NewString()), config, func() {
		stop()
		cancel()
	}, nil
}

func (c OpenIMClient) invoke(ctx context.Context, config *ccontext.GlobalConfig, path string, input, output any) error {
	endpoint, err := endpointURL(config.ApiAddr, path)
	if err != nil {
		return err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal OpenIM request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create OpenIM request: %w", err)
	}
	operationID, _ := ctx.Value("operationID").(string)
	if operationID == "" {
		return errors.New("OpenIM operation ID is required")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("operationID", operationID)
	request.Header.Set("token", config.Token)

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("OpenIM request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("OpenIM request failed with status %d", response.StatusCode)
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read OpenIM response: %w", err)
	}
	var envelope struct {
		ErrCode int             `json:"errCode"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("decode OpenIM response: %w", err)
	}
	if envelope.ErrCode != 0 {
		return fmt.Errorf("OpenIM request failed with code %d", envelope.ErrCode)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, output); err != nil {
		return fmt.Errorf("decode OpenIM response data: %w", err)
	}
	return nil
}

func endpointURL(apiAddr, path string) (string, error) {
	base, err := url.Parse(apiAddr)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("OpenIM API address is invalid")
	}
	return strings.TrimRight(base.String(), "/") + path, nil
}

// OpenIMSourceConfig combines fixed local profile/runtime facts with the
// verified server user endpoint. No value in this config is exported directly
// from the daemon's SDK or database.
type OpenIMSourceConfig struct {
	Profile Profile
	SelfID  string
	Client  Client
	Daemon  func() DaemonStatus
}

// OpenIMSource implements the complete profile read source with daemon-owned
// local state and fixed authenticated server user reads.
type OpenIMSource struct {
	profile Profile
	selfID  string
	client  Client
	daemon  func() DaemonStatus
}

func NewOpenIMSource(config OpenIMSourceConfig) (*OpenIMSource, error) {
	if strings.TrimSpace(config.Profile.ID) == "" || strings.TrimSpace(config.SelfID) == "" || config.Client == nil || config.Daemon == nil {
		return nil, errors.New("profile, self user, client, and daemon source are required")
	}
	return &OpenIMSource{profile: config.Profile, selfID: config.SelfID, client: config.Client, daemon: config.Daemon}, nil
}

func (s *OpenIMSource) Profile(context.Context) (Profile, error) {
	if s == nil {
		return Profile{}, errors.New("OpenIM profile source is required")
	}
	return s.profile, nil
}

func (s *OpenIMSource) Self(ctx context.Context) (User, error) {
	users, err := s.Users(ctx, []string{s.selfID})
	if err != nil {
		return User{}, err
	}
	for _, user := range users {
		if user.ID == s.selfID {
			return user, nil
		}
	}
	return User{}, fmt.Errorf("%w: %s", ErrUserNotFound, s.selfID)
}

func (s *OpenIMSource) Users(ctx context.Context, ids []string) ([]User, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("OpenIM profile source is required")
	}
	return s.client.Users(ctx, append([]string(nil), ids...))
}

func (s *OpenIMSource) Daemon(context.Context) (DaemonStatus, error) {
	if s == nil || s.daemon == nil {
		return DaemonStatus{}, errors.New("OpenIM profile source is required")
	}
	status := s.daemon()
	if status.ProfileID == "" {
		status.ProfileID = s.profile.ID
	}
	if status.ProfileID != s.profile.ID {
		return DaemonStatus{}, errors.New("daemon source returned a different profile")
	}
	return status, nil
}

func (s *OpenIMSource) Doctor(ctx context.Context) (DoctorReport, error) {
	status, err := s.Daemon(ctx)
	if err != nil {
		return DoctorReport{}, err
	}
	report := DoctorReport{Checks: make([]Check, 0, 3)}
	if status.State == "ready" && status.CredentialsValid {
		report.Checks = append(report.Checks, Check{Name: "daemon", Status: "ok"})
	} else {
		report.Checks = append(report.Checks, Check{Name: "daemon", Status: "failed", Detail: "daemon is not ready"})
	}
	if _, err := s.Self(ctx); err != nil {
		report.Checks = append(report.Checks, Check{Name: "server_user", Status: "failed", Detail: "authenticated user read failed"})
		report.Summary = "one or more checks failed"
		return report, nil
	}
	report.Checks = append(report.Checks, Check{Name: "server_user", Status: "ok"})
	for _, check := range report.Checks {
		if check.Status != "ok" {
			report.Summary = "one or more checks failed"
			return report, nil
		}
	}
	report.OK = true
	report.Summary = "all checks passed"
	return report, nil
}

func usersFromSDK(items []*sdkws.UserInfo) []User {
	result := make([]User, 0, len(items))
	for _, item := range items {
		if item == nil || strings.TrimSpace(item.UserID) == "" {
			continue
		}
		result = append(result, User{ID: item.UserID, Name: item.Nickname, Nickname: item.Nickname, Avatar: item.FaceURL})
	}
	return result
}
