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
