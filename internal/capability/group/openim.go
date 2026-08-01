package group

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
	"github.com/abd-im/abd-im-sdk-core/v3/pkg/constant"
	"github.com/google/uuid"
	pbgroup "github.com/openimsdk/protocol/group"
	"github.com/openimsdk/protocol/sdkws"
)

// OpenIMCreator invokes the authenticated server action through the
// daemon-owned SDK context. It deliberately does not call the SDK Group API,
// whose successful path updates local SDK state.
type OpenIMCreator struct {
	Context    func() context.Context
	HTTPClient *http.Client
}

func NewOpenIMCreator(creator OpenIMCreator) (*OpenIMCreator, error) {
	if creator.Context == nil {
		return nil, errors.New("OpenIM SDK context is required")
	}
	return &creator, nil
}

func (c OpenIMCreator) CreateGroup(ctx context.Context, input Input) error {
	request, config, done, err := c.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()

	return c.invoke(request, config, &pbgroup.CreateGroupReq{
		MemberUserIDs: append([]string(nil), input.MemberIDs...),
		GroupInfo:     &sdkws.GroupInfo{GroupName: input.Name, GroupType: constant.WorkingGroup},
		OwnerUserID:   config.UserID,
	})
}

func (c OpenIMCreator) requestContext(caller context.Context) (context.Context, *ccontext.GlobalConfig, func(), error) {
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

func (c OpenIMCreator) invoke(ctx context.Context, config *ccontext.GlobalConfig, input *pbgroup.CreateGroupReq) error {
	endpoint, err := groupCreateEndpoint(config.ApiAddr)
	if err != nil {
		return err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal OpenIM group create request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create OpenIM group create request: %w", err)
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
		return errors.New("OpenIM group create rejected")
	}
	return nil
}

func groupCreateEndpoint(apiAddr string) (string, error) {
	base, err := url.Parse(apiAddr)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("OpenIM API address is invalid")
	}
	return strings.TrimRight(base.String(), "/") + "/group/create_group", nil
}

func unknownOutcome() error {
	return fmt.Errorf("OpenIM group create did not produce a verifiable result: %w", operation.ErrOutcomeUnknown)
}

var _ Creator = (*OpenIMCreator)(nil)
