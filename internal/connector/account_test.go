package connector

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccountLoginUsesABDPasswordProtocolWithoutLeakingCredentials(t *testing.T) {
	const password = "password-marker"
	digest := md5.Sum([]byte(password))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("operationID") == "" {
			t.Fatalf("login request = %s, operationID=%q", request.Method, request.Header.Get("operationID"))
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["phoneNumber"] != "15500000000" || payload["areaCode"] != "+86" || payload["password"] != hex.EncodeToString(digest[:]) || payload["platform"] != float64(ABDPlatformID) {
			t.Fatalf("login payload = %#v", payload)
		}
		encoded, _ := json.Marshal(payload)
		if strings.Contains(string(encoded), password) {
			t.Fatalf("login payload leaked raw password: %s", encoded)
		}
		_, _ = writer.Write([]byte(`{"errCode":0,"data":{"userID":"bot-user","imToken":"token-marker"}}`))
	}))
	defer server.Close()

	userID, token, err := AccountLogin(context.Background(), server.Client(), server.URL, "15500000000", "+86", password)
	if err != nil || userID != "bot-user" || token != "token-marker" {
		t.Fatalf("AccountLogin() = %q, %q, %v", userID, token, err)
	}
}

func TestResolveABDEndpointsUsesEnvironmentOverrides(t *testing.T) {
	values := map[string]string{
		EnvABDAccountLoginURL: " http://127.0.0.1:10008/account/login ",
		EnvABDOpenIMAPIAddr:   "http://127.0.0.1:10002",
		EnvABDOpenIMWSAddr:    "ws://127.0.0.1:10001",
	}
	got := ResolveABDEndpoints(func(name string) string { return values[name] })
	if got.AccountLoginURL != "http://127.0.0.1:10008/account/login" || got.APIAddr != "http://127.0.0.1:10002" || got.WSAddr != "ws://127.0.0.1:10001" {
		t.Fatalf("ResolveABDEndpoints() = %#v", got)
	}

	defaults := ResolveABDEndpoints(nil)
	if defaults.AccountLoginURL != ABDLoginURL || defaults.APIAddr != ABDAPIAddr || defaults.WSAddr != ABDWSAddr {
		t.Fatalf("ResolveABDEndpoints(nil) = %#v", defaults)
	}
}

func TestAccountLoginSupportsEmailAndSanitizesServerFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		if payload["email"] != "bot@example.com" || payload["phoneNumber"] != "" {
			t.Fatalf("email payload = %#v", payload)
		}
		_, _ = writer.Write([]byte(`{"errCode":20001,"errMsg":"PasswordError","data":{"imToken":"must-not-leak"}}`))
	}))
	defer server.Close()

	_, _, err := AccountLogin(context.Background(), server.Client(), server.URL, "bot@example.com", "", "wrong")
	if err == nil || !strings.Contains(err.Error(), "PasswordError") || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("AccountLogin() error = %v", err)
	}
}
