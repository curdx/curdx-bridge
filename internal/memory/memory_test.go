package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Source: claude_code_bridge/test/test_memory_module.py

func TestDedupeMessagesRemovesConsecutiveDuplicates(t *testing.T) {
	d := &ConversationDeduper{}
	// Python's dedupe_messages only removes CONSECUTIVE duplicates
	entries := []ConversationEntry{
		{Role: "user", Content: "hello"},
		{Role: "user", Content: "hello"},         // consecutive duplicate — removed
		{Role: "assistant", Content: "hi there"},
		{Role: "assistant", Content: "hi there"},  // consecutive duplicate — removed
		{Role: "user", Content: "something new"},
	}
	result := d.DedupeMessages(entries)
	if len(result) != 3 {
		t.Errorf("expected 3 entries after dedup, got %d", len(result))
	}
}

func TestStripProtocolMarkers(t *testing.T) {
	d := &ConversationDeduper{}
	text := "hello\nCURDX_REQ_ID: 20260125-143000-123-12345-1\nworld\nCURDX_DONE: 20260125-143000-123-12345-1\n"
	result := d.StripProtocolMarkers(text)
	if result != "hello\nworld\n" {
		t.Errorf("expected markers stripped, got %q", result)
	}
}

func TestStripSystemNoise(t *testing.T) {
	d := &ConversationDeduper{}
	text := "before<system-reminder>noise</system-reminder>after"
	result := d.StripSystemNoise(text)
	if result != "beforeafter" {
		t.Errorf("expected noise stripped, got %q", result)
	}
}

func TestContextFormatterEstimateTokens(t *testing.T) {
	f := NewContextFormatter(8000)
	// 4 chars per token
	if f.EstimateTokens("12345678") != 2 {
		t.Error("expected 2 tokens for 8 chars")
	}
}

func TestContextFormatterTruncateToLimit(t *testing.T) {
	f := NewContextFormatter(8000)
	convs := [][2]string{
		{"short", "reply"},
		{"another", "one"},
	}
	result := f.TruncateToLimit(convs, 5) // very low limit
	if len(result) > len(convs) {
		t.Error("should not exceed input length")
	}
}

func TestParseSessionBasic(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "test.jsonl")

	lines := []string{
		`{"type":"user","message":{"content":"hello"},"uuid":"u1"}`,
		`{"type":"assistant","message":{"content":"hi there"},"uuid":"a1"}`,
	}
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	os.WriteFile(sessionFile, []byte(content), 0o644)

	p := NewClaudeSessionParser(dir)
	entries, err := p.ParseSession(sessionFile)
	if err != nil {
		t.Fatalf("ParseSession failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Role != "user" || entries[0].Content != "hello" {
		t.Errorf("first entry: role=%q content=%q", entries[0].Role, entries[0].Content)
	}
	if entries[1].Role != "assistant" || entries[1].Content != "hi there" {
		t.Errorf("second entry: role=%q content=%q", entries[1].Role, entries[1].Content)
	}
}

func TestParseSessionWithToolCalls(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "test.jsonl")

	msg := map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": "let me check"},
			map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"file_path": "/tmp/test.go"}},
		},
	}
	entry := map[string]any{
		"type":    "assistant",
		"message": msg,
		"uuid":    "a1",
	}
	b, _ := json.Marshal(entry)
	os.WriteFile(sessionFile, append(b, '\n'), 0o644)

	p := NewClaudeSessionParser(dir)
	entries, err := p.ParseSession(sessionFile)
	if err != nil {
		t.Fatalf("ParseSession failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Content != "let me check" {
		t.Errorf("content=%q", entries[0].Content)
	}
	if len(entries[0].ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(entries[0].ToolCalls))
	}
}

func TestFormatMarkdown(t *testing.T) {
	f := NewContextFormatter(8000)
	ctx := &TransferContext{
		Conversations:   [][2]string{{"hello", "world"}},
		SourceSessionID: "test-session",
		SourceProvider:  "claude",
	}
	result := f.FormatMarkdown(ctx, false)
	if result == "" {
		t.Error("expected non-empty markdown")
	}
	if !contains(result, "Claude") {
		t.Error("should mention Claude")
	}
	if !contains(result, "hello") {
		t.Error("should contain user message")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
