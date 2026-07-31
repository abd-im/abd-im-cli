//go:build unix

package provider

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestBridgeServesOneRunPrivateMCPConnection(t *testing.T) {
	proxy := &recordingProxy{}
	server, err := New("work", "daemon-grant", proxy, DefaultTools([]string{"message.history"}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	socket := filepath.Join(t.TempDir(), "provider.sock")
	bridge, err := StartBridge(socket, server)
	if err != nil {
		t.Fatalf("StartBridge() error = %v", err)
	}
	t.Cleanup(func() { _ = bridge.Close() })
	info, err := os.Stat(socket)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("provider socket mode = %v, %v", info.Mode(), err)
	}

	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatalf("DialUnix() error = %v", err)
	}
	defer connection.Close()
	encoder := json.NewEncoder(connection)
	decoder := bufio.NewReader(connection)
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2026-07-28", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "test", "version": "v1"}}}); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if _, err := decoder.ReadBytes('\n'); err != nil {
		t.Fatalf("read initialize: %v", err)
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "abdim.message.history", "arguments": map[string]any{"conversation_id": "conversation-1", "limit": 1}}}); err != nil {
		t.Fatalf("write tools/call: %v", err)
	}
	if _, err := decoder.ReadBytes('\n'); err != nil {
		t.Fatalf("read tools/call: %v", err)
	}
	if len(proxy.calls) != 1 || proxy.calls[0].Grant != "daemon-grant" || proxy.calls[0].Method != "message.history" {
		t.Fatalf("run proxy calls = %+v", proxy.calls)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close bridge connection: %v", err)
	}
	if err := bridge.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("provider socket still exists: %v", err)
	}
}
