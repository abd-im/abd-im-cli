package blacklist

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

	"github.com/abd-im/abd-im-cli/internal/operation"
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/ccontext"
	"github.com/google/uuid"
	"github.com/openimsdk/protocol/relation"
)

// OpenIMSource invokes only fixed blacklist read/action endpoints through the
// daemon-owned authenticated SDK context. It never exposes an SDK relation API
// or its local synchronization path.
type OpenIMSource struct {
	Context    func() context.Context
	HTTPClient *http.Client
}

func NewOpenIMSource(source OpenIMSource) (*OpenIMSource, error) {
	if source.Context == nil {
		return nil, errors.New("OpenIM SDK context is required")
	}
	return &source, nil
}

func (s OpenIMSource) AddBlacklist(ctx context.Context, userID string) error {
	request, config, done, err := s.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	return s.invokeAction(request, config, "/friend/add_black", &relation.AddBlackReq{
		OwnerUserID: config.UserID,
		BlackUserID: userID,
	})
}

func (s OpenIMSource) RemoveBlacklist(ctx context.Context, userID string) error {
	request, config, done, err := s.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	return s.invokeAction(request, config, "/friend/remove_black", &relation.RemoveBlackReq{
		OwnerUserID: config.UserID,
		BlackUserID: userID,
	})
}

func (s OpenIMSource) IsBlacklisted(ctx context.Context, userID string) (bool, error) {
	request, config, done, err := s.requestContext(ctx)
	if err != nil {
		return false, err
	}
	defer done()
	var response relation.GetSpecifiedBlacksResp
	if err := s.invokeRead(request, config, "/friend/get_specified_blacks", &relation.GetSpecifiedBlacksReq{
		OwnerUserID: config.UserID,
		UserIDList:  []string{userID},
	}, &response); err != nil {
		return false, err
	}
	for _, item := range response.Blacks {
		if item != nil && item.BlackUserInfo != nil && item.BlackUserInfo.UserID == userID {
			return true, nil
		}
	}
	return false, nil
}

func (s OpenIMSource) requestContext(caller context.Context) (context.Context, *ccontext.GlobalConfig, func(), error) {
	if caller == nil {
		return nil, nil, nil, errors.New("caller context is required")
	}
	if err := caller.Err(); err != nil {
		return nil, nil, nil, err
	}
	base := s.Context()
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

func (s OpenIMSource) invokeAction(ctx context.Context, config *ccontext.GlobalConfig, path string, input any) error {
	endpoint, err := blacklistEndpoint(config.ApiAddr, path)
	if err != nil {
		return err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("encode OpenIM blacklist request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("create OpenIM blacklist request")
	}
	operationID, _ := ctx.Value("operationID").(string)
	if operationID == "" {
		return errors.New("OpenIM operation ID is required")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("operationID", operationID)
	request.Header.Set("token", config.Token)

	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return unknownOutcome()
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return unknownOutcome()
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return unknownOutcome()
	}
	var envelope struct {
		ErrCode int `json:"errCode"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return unknownOutcome()
	}
	if envelope.ErrCode != 0 {
		return errors.New("OpenIM blacklist action rejected")
	}
	return nil
}

func (s OpenIMSource) invokeRead(ctx context.Context, config *ccontext.GlobalConfig, path string, input, output any) error {
	endpoint, err := blacklistEndpoint(config.ApiAddr, path)
	if err != nil {
		return err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("encode OpenIM blacklist read")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("create OpenIM blacklist read")
	}
	operationID, _ := ctx.Value("operationID").(string)
	if operationID == "" {
		return errors.New("OpenIM operation ID is required")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("operationID", operationID)
	request.Header.Set("token", config.Token)

	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("OpenIM blacklist read failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("OpenIM blacklist read failed with status %d", response.StatusCode)
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return errors.New("read OpenIM blacklist response")
	}
	var envelope struct {
		ErrCode int             `json:"errCode"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return errors.New("decode OpenIM blacklist response")
	}
	if envelope.ErrCode != 0 {
		return errors.New("OpenIM blacklist read rejected")
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, output); err != nil {
		return errors.New("decode OpenIM blacklist response data")
	}
	return nil
}

func blacklistEndpoint(apiAddr, path string) (string, error) {
	base, err := url.Parse(apiAddr)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("OpenIM API address is invalid")
	}
	return strings.TrimRight(base.String(), "/") + path, nil
}

func unknownOutcome() error {
	return fmt.Errorf("OpenIM blacklist action did not produce a verifiable result: %w", operation.ErrOutcomeUnknown)
}

var _ Source = (*OpenIMSource)(nil)
