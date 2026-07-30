// Package logsafe provides the logging boundary for data that may originate
// outside the daemon. Callers must use Logger rather than writing SDK or
// transport diagnostics directly to stderr or an audit sink.
package logsafe

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

const (
	redacted    = "[REDACTED]"
	redactedURL = "[REDACTED_URL]"
)

var (
	urlPattern           = regexp.MustCompile(`(?i)\b(?:https?|wss?)://[^\s"'<>]+`)
	requestPattern       = regexp.MustCompile(`(?im)(["']?\brequest\b["']?\s*[:=]\s*)(.*)$`)
	authorizationPattern = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer\s+)?)("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;&}\]]+)`)
	keyPattern           = regexp.MustCompile(`(?i)((?:["']?(?:access[_-]?token|api[_-]?key|authorization|credential|password|secret|signature|sig|token)["']?)\s*[:=]\s*)("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;&}\]]+)`)
)

// Redactor removes registered secrets and common credential-bearing values
// from diagnostic text. It is safe to reuse concurrently after construction.
type Redactor struct {
	secrets []string
}

// NewRedactor creates a redactor for values that must never be emitted. Empty
// values are ignored so a missing credential cannot redact every log message.
func NewRedactor(secrets ...string) Redactor {
	unique := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		if secret != "" {
			unique[secret] = struct{}{}
		}
	}

	values := make([]string, 0, len(unique))
	for secret := range unique {
		values = append(values, secret)
	}
	// Redact longer values first when one credential is a prefix of another.
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })

	return Redactor{secrets: values}
}

// Redact removes registered secrets, credential-like key/value pairs, and
// HTTP or WebSocket URLs from text intended for a log sink.
func (r Redactor) Redact(value string) string {
	for _, secret := range r.secrets {
		value = strings.ReplaceAll(value, secret, redacted)
	}
	value = urlPattern.ReplaceAllString(value, redactedURL)
	value = requestPattern.ReplaceAllString(value, "$1"+redacted)
	value = authorizationPattern.ReplaceAllStringFunc(value, redactKeyValue)
	return keyPattern.ReplaceAllStringFunc(value, redactKeyValue)
}

// URL returns an opaque representation suitable for transport diagnostics.
// URLs can contain credentials, request material, and internal paths, so no
// part of a non-empty URL is retained.
func (r Redactor) URL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	return redactedURL
}

func redactKeyValue(match string) string {
	index := strings.IndexAny(match, ":=")
	if index == -1 {
		return redacted
	}

	prefix := match[:index+1]
	value := strings.TrimSpace(match[index+1:])
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		return prefix + string(value[0]) + redacted + string(value[0])
	}
	return prefix + redacted
}

// Logger writes redacted diagnostic records to one output. The surface is
// included so consumers can route or filter logs without handling secrets.
type Logger struct {
	out      io.Writer
	redactor Redactor
}

// NewLogger creates a logger that writes one redacted record per call.
func NewLogger(out io.Writer, secrets ...string) Logger {
	return Logger{out: out, redactor: NewRedactor(secrets...)}
}

// SDK writes an SDK diagnostic record.
func (l Logger) SDK(format string, args ...any) {
	l.write("sdk", format, args...)
}

// HTTP writes an HTTP diagnostic record.
func (l Logger) HTTP(format string, args ...any) {
	l.write("http", format, args...)
}

// WebSocket writes a WebSocket diagnostic record.
func (l Logger) WebSocket(format string, args ...any) {
	l.write("websocket", format, args...)
}

// Stderr writes a stderr diagnostic record.
func (l Logger) Stderr(format string, args ...any) {
	l.write("stderr", format, args...)
}

// Audit writes an audit diagnostic record. It deliberately accepts only a
// formatted summary; request bodies and arbitrary audit payloads are not a
// supported logging input.
func (l Logger) Audit(format string, args ...any) {
	l.write("audit", format, args...)
}

func (l Logger) write(surface, format string, args ...any) {
	if l.out == nil {
		return
	}
	_, _ = fmt.Fprintf(l.out, "%s: %s\n", surface, l.redactor.Redact(fmt.Sprintf(format, args...)))
}
