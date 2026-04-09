package comm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// =========================================================================
// GeminiLogReader tests (ported from test_gemini_comm.py)
// =========================================================================

func writeGeminiSession(t *testing.T, path string, messages []map[string]interface{}, sessionID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := map[string]interface{}{
		"sessionId": sessionID,
		"messages":  messages,
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGemini_CaptureStateFindsSluifiedSuffixProjectHash(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "claude_code_bridge")
	os.MkdirAll(workDir, 0o755)
	root := filepath.Join(dir, "gemini-root")
	sessionPath := filepath.Join(root, "claude-code-bridge-1", "chats", "session-a.json")
	writeGeminiSession(t, sessionPath, []map[string]interface{}{
		{"type": "user", "content": "hello"},
		{"type": "gemini", "id": "g1", "content": "world"},
	}, "sid-1")

	reader := NewGeminiLogReader(root, workDir)
	state := reader.CaptureState()

	if state.SessionPath != sessionPath {
		t.Errorf("expected session_path=%q, got %q", sessionPath, state.SessionPath)
	}
	if state.MsgCount != 2 {
		t.Errorf("expected msg_count=2, got %d", state.MsgCount)
	}
}

func TestGemini_WaitForMessageReadsReply(t *testing.T) {
	reqID := "20260222-161452-539-76463-1"
	dir := t.TempDir()
	workDir := filepath.Join(dir, "claude_code_bridge")
	os.MkdirAll(workDir, 0o755)
	root := filepath.Join(dir, "gemini-root")
	sessionPath := filepath.Join(root, "claude-code-bridge-1", "chats", "session-b.json")

	messages := []map[string]interface{}{
		{"type": "user", "content": fmt.Sprintf("CCB_REQ_ID: %s\nquestion", reqID)},
	}
	writeGeminiSession(t, sessionPath, messages, "sid-2")

	reader := NewGeminiLogReader(root, workDir)
	state := reader.CaptureState()

	// Add gemini reply
	messages = append(messages, map[string]interface{}{
		"type":    "gemini",
		"id":      "g2",
		"content": fmt.Sprintf("ok\nCCB_DONE: %s", reqID),
	})
	writeGeminiSession(t, sessionPath, messages, "sid-2")

	reply, newState := reader.WaitForMessage(state, 500*time.Millisecond)

	if reply == "" {
		t.Fatal("expected non-empty reply")
	}
	if !strings.Contains(reply, "CCB_DONE: "+reqID) {
		t.Errorf("expected CCB_DONE marker in reply, got %q", reply)
	}
	if newState.SessionPath != sessionPath {
		t.Errorf("expected session_path=%q, got %q", sessionPath, newState.SessionPath)
	}
}

func TestGemini_LatestMessage(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "myproject")
	os.MkdirAll(workDir, 0o755)
	root := filepath.Join(dir, "gemini-root")
	sessionPath := filepath.Join(root, "myproject", "chats", "session-c.json")
	writeGeminiSession(t, sessionPath, []map[string]interface{}{
		{"type": "user", "content": "hi"},
		{"type": "gemini", "id": "g1", "content": "first reply"},
		{"type": "user", "content": "another"},
		{"type": "gemini", "id": "g2", "content": "second reply"},
	}, "sid-3")

	reader := NewGeminiLogReader(root, workDir)
	msg := reader.LatestMessage()

	if msg != "second reply" {
		t.Errorf("expected 'second reply', got %q", msg)
	}
}

func TestGemini_LatestConversations(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "myproject")
	os.MkdirAll(workDir, 0o755)
	root := filepath.Join(dir, "gemini-root")
	sessionPath := filepath.Join(root, "myproject", "chats", "session-d.json")
	writeGeminiSession(t, sessionPath, []map[string]interface{}{
		{"type": "user", "content": "q1"},
		{"type": "gemini", "id": "g1", "content": "a1"},
		{"type": "user", "content": "q2"},
		{"type": "gemini", "id": "g2", "content": "a2"},
	}, "sid-4")

	reader := NewGeminiLogReader(root, workDir)
	pairs := reader.LatestConversations(2)

	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(pairs))
	}
	if pairs[0].User != "q1" || pairs[0].Assistant != "a1" {
		t.Errorf("pair 0: got %+v", pairs[0])
	}
	if pairs[1].User != "q2" || pairs[1].Assistant != "a2" {
		t.Errorf("pair 1: got %+v", pairs[1])
	}
}
