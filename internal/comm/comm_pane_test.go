package comm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeReqID generates a unique test request ID.
func makeReqID() string {
	now := time.Now()
	ms := now.Nanosecond() / 1_000_000
	return fmt.Sprintf("%s-%03d-%d-1",
		now.Format("20060102-150405"),
		ms,
		os.Getpid(),
	)
}

func writePaneLog(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "pane.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// =========================================================================
// CodebuddyLogReader tests (ported from test_codebuddy_comm.py)
// =========================================================================

func TestCodebuddy_CaptureStateReturnsOffset(t *testing.T) {
	dir := t.TempDir()
	logPath := writePaneLog(t, dir, "hello world\n")

	reader := NewCodebuddyLogReader(dir, logPath)
	state := reader.CaptureState()

	if state.PaneLogPath != logPath {
		t.Errorf("expected pane_log_path=%q, got %q", logPath, state.PaneLogPath)
	}
	if state.Offset <= 0 {
		t.Errorf("expected offset > 0, got %d", state.Offset)
	}
}

func TestCodebuddy_CaptureStateNoLog(t *testing.T) {
	dir := t.TempDir()
	reader := NewCodebuddyLogReader(dir, filepath.Join(dir, "nonexistent.log"))
	state := reader.CaptureState()

	if state.Offset != 0 {
		t.Errorf("expected offset=0, got %d", state.Offset)
	}
}

func TestCodebuddy_LatestMessageExtractsAssistantBlock(t *testing.T) {
	dir := t.TempDir()
	reqID := makeReqID()
	content := fmt.Sprintf("CCB_REQ_ID: %s\nThis is the reply\nCCB_DONE: %s\n", reqID, reqID)
	logPath := writePaneLog(t, dir, content)

	reader := NewCodebuddyLogReader(dir, logPath)
	message := reader.LatestMessage()

	if message == "" {
		t.Fatal("expected non-empty message")
	}
	if !strings.Contains(message, "This is the reply") {
		t.Errorf("expected message to contain 'This is the reply', got %q", message)
	}
}

func TestCodebuddy_LatestMessageStripsAnsi(t *testing.T) {
	dir := t.TempDir()
	reqID := makeReqID()
	content := fmt.Sprintf("CCB_REQ_ID: %s\n\x1b[32mcolored text\x1b[0m\nCCB_DONE: %s\n", reqID, reqID)
	logPath := writePaneLog(t, dir, content)

	reader := NewCodebuddyLogReader(dir, logPath)
	message := reader.LatestMessage()

	if message == "" {
		t.Fatal("expected non-empty message")
	}
	if !strings.Contains(message, "colored text") {
		t.Errorf("expected message to contain 'colored text', got %q", message)
	}
	if strings.Contains(message, "\x1b") {
		t.Error("expected message to not contain ANSI escape sequences")
	}
}

func TestCodebuddy_LatestMessageReturnsEmptyWhenNoLog(t *testing.T) {
	dir := t.TempDir()
	reader := NewCodebuddyLogReader(dir, filepath.Join(dir, "nonexistent.log"))
	if msg := reader.LatestMessage(); msg != "" {
		t.Errorf("expected empty message, got %q", msg)
	}
}

func TestCodebuddy_WaitForMessageDetectsNewContent(t *testing.T) {
	dir := t.TempDir()
	reqID := makeReqID()
	logPath := writePaneLog(t, dir, "initial content\n")

	reader := NewCodebuddyLogReader(dir, logPath)
	state := reader.CaptureState()

	// Append new content with CCB markers
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, "CCB_REQ_ID: %s\nreply text\nCCB_DONE: %s\n", reqID, reqID)
	f.Close()

	message, newState := reader.WaitForMessage(state, 500*time.Millisecond)
	if message == "" {
		t.Fatal("expected non-empty message")
	}
	if !strings.Contains(message, "reply text") {
		t.Errorf("expected 'reply text' in message, got %q", message)
	}
	if newState.Offset <= state.Offset {
		t.Errorf("expected new offset > old offset")
	}
}

func TestCodebuddy_TryGetMessageNonblocking(t *testing.T) {
	dir := t.TempDir()
	logPath := writePaneLog(t, dir, "content\n")

	reader := NewCodebuddyLogReader(dir, logPath)
	state := reader.CaptureState()

	message, _ := reader.TryGetMessage(state)
	if message != "" {
		t.Errorf("expected empty message for no new content, got %q", message)
	}
}

func TestCodebuddy_LatestConversationsExtractsPairs(t *testing.T) {
	dir := t.TempDir()
	reqID := makeReqID()
	content := fmt.Sprintf("user prompt here\nCCB_REQ_ID: %s\nassistant reply here\nCCB_DONE: %s\n", reqID, reqID)
	logPath := writePaneLog(t, dir, content)

	reader := NewCodebuddyLogReader(dir, logPath)
	pairs := reader.LatestConversations(1)

	if len(pairs) < 1 {
		t.Fatal("expected at least 1 conversation pair")
	}
	last := pairs[len(pairs)-1]
	if !strings.Contains(last.Assistant, "assistant reply here") {
		t.Errorf("expected assistant text, got %q", last.Assistant)
	}
}

func TestCodebuddy_WaitForEventsReturnsEvents(t *testing.T) {
	dir := t.TempDir()
	reqID := makeReqID()
	logPath := writePaneLog(t, dir, "")

	reader := NewCodebuddyLogReader(dir, logPath)
	state := reader.CaptureState()

	f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	fmt.Fprintf(f, "prompt\nCCB_REQ_ID: %s\nreply\nCCB_DONE: %s\n", reqID, reqID)
	f.Close()

	events, _ := reader.WaitForEvents(state, 500*time.Millisecond)
	if len(events) == 0 {
		t.Fatal("expected events")
	}
	foundAssistant := false
	for _, e := range events {
		if e.Role == "assistant" {
			foundAssistant = true
		}
	}
	if !foundAssistant {
		t.Error("expected an assistant event")
	}
}

func TestCodebuddy_SetPaneLogPath(t *testing.T) {
	dir := t.TempDir()
	reader := NewCodebuddyLogReader(dir, "")
	logPath := writePaneLog(t, dir, "test\n")

	reader.SetPaneLogPath(logPath)
	if reader.paneLogPath != logPath {
		t.Errorf("expected paneLogPath=%q, got %q", logPath, reader.paneLogPath)
	}
}

func TestCodebuddy_LogTruncationResetsOffset(t *testing.T) {
	dir := t.TempDir()
	logPath := writePaneLog(t, dir, strings.Repeat("a", 1000)+"\n")

	reader := NewCodebuddyLogReader(dir, logPath)
	state := reader.CaptureState()
	if state.Offset <= 500 {
		t.Fatalf("expected offset > 500, got %d", state.Offset)
	}

	// Truncate the log
	os.WriteFile(logPath, []byte("short\n"), 0o644)

	_, newState := reader.TryGetMessage(state)
	if newState.Offset >= state.Offset {
		t.Errorf("expected offset to decrease after truncation")
	}
}

// =========================================================================
// QwenLogReader tests (ported from test_qwen_comm.py)
// =========================================================================

func TestQwen_CaptureStateReturnsOffset(t *testing.T) {
	dir := t.TempDir()
	logPath := writePaneLog(t, dir, "hello world\n")

	reader := NewQwenLogReader(dir, logPath)
	state := reader.CaptureState()

	if state.PaneLogPath != logPath {
		t.Errorf("expected pane_log_path=%q, got %q", logPath, state.PaneLogPath)
	}
	if state.Offset <= 0 {
		t.Errorf("expected offset > 0, got %d", state.Offset)
	}
}

func TestQwen_CaptureStateNoLog(t *testing.T) {
	dir := t.TempDir()
	reader := NewQwenLogReader(dir, filepath.Join(dir, "nonexistent.log"))
	state := reader.CaptureState()

	if state.Offset != 0 {
		t.Errorf("expected offset=0, got %d", state.Offset)
	}
}

func TestQwen_LatestMessageExtractsAssistantBlock(t *testing.T) {
	dir := t.TempDir()
	reqID := makeReqID()
	content := fmt.Sprintf("CCB_REQ_ID: %s\nThis is the reply\nCCB_DONE: %s\n", reqID, reqID)
	logPath := writePaneLog(t, dir, content)

	reader := NewQwenLogReader(dir, logPath)
	message := reader.LatestMessage()

	if message == "" {
		t.Fatal("expected non-empty message")
	}
	if !strings.Contains(message, "This is the reply") {
		t.Errorf("expected message to contain 'This is the reply', got %q", message)
	}
}

func TestQwen_LatestMessageStripsAnsi(t *testing.T) {
	dir := t.TempDir()
	reqID := makeReqID()
	content := fmt.Sprintf("CCB_REQ_ID: %s\n\x1b[32mcolored text\x1b[0m\nCCB_DONE: %s\n", reqID, reqID)
	logPath := writePaneLog(t, dir, content)

	reader := NewQwenLogReader(dir, logPath)
	message := reader.LatestMessage()

	if message == "" {
		t.Fatal("expected non-empty message")
	}
	if !strings.Contains(message, "colored text") {
		t.Errorf("expected 'colored text', got %q", message)
	}
	if strings.Contains(message, "\x1b") {
		t.Error("expected no ANSI escapes")
	}
}

func TestQwen_LatestMessageReturnsEmptyWhenNoLog(t *testing.T) {
	dir := t.TempDir()
	reader := NewQwenLogReader(dir, filepath.Join(dir, "nonexistent.log"))
	if msg := reader.LatestMessage(); msg != "" {
		t.Errorf("expected empty, got %q", msg)
	}
}

func TestQwen_WaitForMessageDetectsNewContent(t *testing.T) {
	dir := t.TempDir()
	reqID := makeReqID()
	logPath := writePaneLog(t, dir, "initial content\n")

	reader := NewQwenLogReader(dir, logPath)
	state := reader.CaptureState()

	f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	fmt.Fprintf(f, "CCB_REQ_ID: %s\nreply text\nCCB_DONE: %s\n", reqID, reqID)
	f.Close()

	message, newState := reader.WaitForMessage(state, 500*time.Millisecond)
	if message == "" {
		t.Fatal("expected non-empty message")
	}
	if !strings.Contains(message, "reply text") {
		t.Errorf("expected 'reply text', got %q", message)
	}
	if newState.Offset <= state.Offset {
		t.Error("expected new offset > old offset")
	}
}

func TestQwen_TryGetMessageNonblocking(t *testing.T) {
	dir := t.TempDir()
	logPath := writePaneLog(t, dir, "content\n")

	reader := NewQwenLogReader(dir, logPath)
	state := reader.CaptureState()

	message, _ := reader.TryGetMessage(state)
	if message != "" {
		t.Errorf("expected empty, got %q", message)
	}
}

func TestQwen_LatestConversationsExtractsPairs(t *testing.T) {
	dir := t.TempDir()
	reqID := makeReqID()
	content := fmt.Sprintf("user prompt here\nCCB_REQ_ID: %s\nassistant reply here\nCCB_DONE: %s\n", reqID, reqID)
	logPath := writePaneLog(t, dir, content)

	reader := NewQwenLogReader(dir, logPath)
	pairs := reader.LatestConversations(1)

	if len(pairs) < 1 {
		t.Fatal("expected at least 1 pair")
	}
	if !strings.Contains(pairs[len(pairs)-1].Assistant, "assistant reply here") {
		t.Errorf("expected assistant reply, got %q", pairs[len(pairs)-1].Assistant)
	}
}

func TestQwen_LogTruncationResetsOffset(t *testing.T) {
	dir := t.TempDir()
	logPath := writePaneLog(t, dir, strings.Repeat("a", 1000)+"\n")

	reader := NewQwenLogReader(dir, logPath)
	state := reader.CaptureState()
	if state.Offset <= 500 {
		t.Fatalf("expected offset > 500, got %d", state.Offset)
	}

	os.WriteFile(logPath, []byte("short\n"), 0o644)

	_, newState := reader.TryGetMessage(state)
	if newState.Offset >= state.Offset {
		t.Error("expected offset to decrease")
	}
}
