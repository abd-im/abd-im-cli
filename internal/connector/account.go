package connector

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ABDLoginURL         = "https://2.alissa.xin/chat/account/login"
	ABDAPIAddr          = "https://2.alissa.xin/api"
	ABDWSAddr           = "wss://2.alissa.xin/msg_gateway"
	ABDPlatformID int32 = 7

	EnvABDAccountLoginURL = "ABDIM_ACCOUNT_LOGIN_URL"
	EnvABDOpenIMAPIAddr   = "ABDIM_OPENIM_API_ADDR"
	EnvABDOpenIMWSAddr    = "ABDIM_OPENIM_WS_ADDR"
)

type ABDEndpoints struct {
	AccountLoginURL string
	APIAddr         string
	WSAddr          string
}

func ResolveABDEndpoints(getenv func(string) string) ABDEndpoints {
	resolve := func(name, fallback string) string {
		if getenv == nil {
			return fallback
		}
		if value := strings.TrimSpace(getenv(name)); value != "" {
			return value
		}
		return fallback
	}
	return ABDEndpoints{
		AccountLoginURL: resolve(EnvABDAccountLoginURL, ABDLoginURL),
		APIAddr:         resolve(EnvABDOpenIMAPIAddr, ABDAPIAddr),
		WSAddr:          resolve(EnvABDOpenIMWSAddr, ABDWSAddr),
	}
}

// AccountLogin exchanges an ABD account password for the OpenIM identity used
// by the daemon. The password is never returned or persisted.
func AccountLogin(ctx context.Context, client *http.Client, loginURL, account, areaCode, password string) (userID, token string, err error) {
	account = strings.TrimSpace(account)
	areaCode = strings.TrimSpace(areaCode)
	if account == "" || password == "" {
		return "", "", errors.New("account and password are required")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if strings.TrimSpace(loginURL) == "" {
		loginURL = ABDLoginURL
	}

	digest := md5.Sum([]byte(password)) // ABD account API protocol compatibility.
	payload := struct {
		PhoneNumber string `json:"phoneNumber"`
		AreaCode    string `json:"areaCode"`
		Password    string `json:"password"`
		Email       string `json:"email"`
		VerifyCode  string `json:"verifyCode"`
		DeviceID    string `json:"deviceID"`
		Platform    int32  `json:"platform"`
		Account     string `json:"account"`
	}{Password: hex.EncodeToString(digest[:]), Platform: ABDPlatformID}
	if strings.Contains(account, "@") {
		payload.Email = account
	} else {
		if areaCode == "" {
			areaCode = "+86"
		}
		payload.PhoneNumber = account
		payload.AreaCode = areaCode
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, bytes.NewReader(body))
	if err != nil {
		return "", "", errors.New("create ABD login request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("operationID", "abdim-login-"+uuid.NewString())
	response, err := client.Do(request)
	if err != nil {
		return "", "", errors.New("ABD login is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", fmt.Errorf("ABD login failed with HTTP %d", response.StatusCode)
	}
	var result struct {
		ErrCode int    `json:"errCode"`
		ErrMsg  string `json:"errMsg"`
		Data    struct {
			UserID  string `json:"userID"`
			IMToken string `json:"imToken"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&result); err != nil {
		return "", "", errors.New("decode ABD login response")
	}
	if result.ErrCode != 0 {
		message := strings.TrimSpace(result.ErrMsg)
		if message == "" {
			message = "account login failed"
		}
		return "", "", fmt.Errorf("ABD login failed: %s (%d)", message, result.ErrCode)
	}
	if strings.TrimSpace(result.Data.UserID) == "" || strings.TrimSpace(result.Data.IMToken) == "" {
		return "", "", errors.New("ABD login returned no OpenIM identity")
	}
	return result.Data.UserID, result.Data.IMToken, nil
}
