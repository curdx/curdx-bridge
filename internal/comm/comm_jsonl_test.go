package comm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =========================================================================
// CodexLogReader tests
// =========================================================================

func writeCodexLog(t *testing.T, dir string, entries []map[string]interface{}) string {
	t.Helper()
	logDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "test-session.jsonl")
	var lines []string
	for _, entry := range entries {
		data, _ := json.Marshal(entry)
		lines = append(lines, string(data))
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return logPath
}

func TestCodex_LatestMessageExtractsReply(t *testing.T) {
	dir := t.TempDir()
	logPath := writeCodexLog(t, dir, []map[string]interface{}{
		{
			"type": "response_item",
			"payload": map[string]interface{}{
				"type": "message",
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "output_text", "text": "Hello from Codex"},
				},
			},
		},
	})

	reader := NewCodexLogReader(filepath.Dir(logPath), logPath, "", "")
	msg := reader.LatestMessage()

	if msg == "" {
		t.Fatal("expected non-empty message")
	}
	if !strings.Contains(msg, "Hello from Codex") {
		t.Errorf("expected 'Hello from Codex', got %q", msg)
	}
}

func TestCodex_LatestMessageSkipsUserMessages(t *testing.T) {
	dir := t.TempDir()
	logPath := writeCodexLog(t, dir, []map[string]interface{}{
		{
			"type": "response_item",
			"payload": map[string]interface{}{
				"type": "message",
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "input_text", "text": "user question"},
				},
			},
		},
		{
			"type": "response_item",
			"payload": map[string]interface{}{
				"type": "message",
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "output_text", "text": "assistant reply"},
				},
			},
		},
	})

	reader := NewCodexLogReader(filepath.Dir(logPath), logPath, "", "")
	msg := reader.LatestMessage()

	if msg != "assistant reply" {
		t.Errorf("expected 'assistant reply', got %q", msg)
	}
}

func TestCodex_ExtractSessionID(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "a1b2c3d4-e5f6-7890-abcd-ef1234567890.jsonl")
	os.WriteFile(logPath, []byte("{}\n"), 0o644)

	sid := ExtractSessionID(logPath)
	if sid != "a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
		t.Errorf("expected UUID, got %q", sid)
	}
}

func TestCodex_LatestConversations(t *testing.T) {
	dir := t.TempDir()
	logPath := writeCodexLog(t, dir, []map[string]interface{}{
		{
			"type": "event_msg",
			"payload": map[string]interface{}{
				"type":    "user_message",
				"message": "What is Go?",
			},
		},
		{
			"type": "response_item",
			"payload": map[string]interface{}{
				"type": "message",
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "output_text", "text": "Go is a programming language."},
				},
			},
		},
	})

	reader := NewCodexLogReader(filepath.Dir(logPath), logPath, "", "")
	pairs := reader.LatestConversations(1)

	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	if pairs[0].User != "What is Go?" {
		t.Errorf("expected user='What is Go?', got %q", pairs[0].User)
	}
	if pairs[0].Assistant != "Go is a programming language." {
		t.Errorf("expected assistant reply, got %q", pairs[0].Assistant)
	}
}


