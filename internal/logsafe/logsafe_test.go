package logsafe

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerRedactsTokenFromEverySurface(t *testing.T) {
	const token = "test-token-marker-4d2a0d"

	var output bytes.Buffer
	logger := NewLogger(&output, token)
	logger.SDK("login failed: token=%s", token)
	logger.HTTP("request https://api.example.test/login?access_token=%s", token)
	logger.WebSocket("connected wss://socket.example.test/connect?token=%s", token)
	logger.Stderr("authorization: Bearer %s", token)
	logger.Audit("request={\"credential\":%q,\"token\":%q}", token, token)

	logs := output.String()
	if strings.Contains(logs, token) {
		t.Fatalf("log output contains token marker: %q", logs)
	}
	for _, surface := range []string{"sdk:", "http:", "websocket:", "stderr:", "audit:"} {
		if !strings.Contains(logs, surface) {
			t.Errorf("log output missing %s record: %q", surface, logs)
		}
	}
}

func TestRedactorRemovesCredentialValuesWithoutRegistration(t *testing.T) {
	redactor := NewRedactor()
	input := `token=secret authorization: Bearer bearer-secret {"api_key":"key-secret"} request=message-body`
	got := redactor.Redact(input)

	for _, secret := range []string{"secret", "bearer-secret", "key-secret", "message-body"} {
		if strings.Contains(got, secret) {
			t.Errorf("Redact(%q) retained %q: %q", input, secret, got)
		}
	}
}

func TestRedactorRedactsURLs(t *testing.T) {
	redactor := NewRedactor("test-token-marker")
	got := redactor.Redact("request to https://api.example.test/path?token=test-token-marker failed")
	if strings.Contains(got, "https://") || strings.Contains(got, "test-token-marker") {
		t.Fatalf("Redact retained URL or token: %q", got)
	}
	if gotURL := redactor.URL("wss://socket.example.test/connect?token=test-token-marker"); gotURL != redactedURL {
		t.Fatalf("URL() = %q, want %q", gotURL, redactedURL)
	}
}
