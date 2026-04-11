package provprotocol

import (
	"fmt"
	"strings"
	"testing"
)

// Source: claude_code_bridge/test/test_protocol.py

// makeTestReqID creates a fake req_id for testing.
func makeTestReqID(n int) string {
	return fmt.Sprintf("20260125-143000-123-12345-%d", n)
}

// stubStripDone is a simple strip done implementation for testing.
func stubStripDone(text, reqID string) string {
	lines := strings.Split(text, "\n")
	var result []string
	doneRe := fmt.Sprintf("CURDX_DONE: %s", reqID)
	for _, ln := range lines {
		if strings.TrimSpace(ln) == doneRe {
			continue
		}
		result = append(result, ln)
	}
	return strings.TrimRight(strings.Join(result, "\n"), " \t\n\r")
}

func TestWrapGeminiPromptStructure(t *testing.T) {
	reqID := makeTestReqID(4)
	prompt := WrapGeminiPrompt("analyze this", reqID)

	if !strings.Contains(prompt, ReqIDPrefix+" "+reqID) {
		t.Error("should contain REQ_ID line")
	}
	if !strings.Contains(prompt, "IMPORTANT") {
		t.Error("should contain IMPORTANT")
	}
	if !strings.Contains(prompt, DonePrefix+" "+reqID) {
		t.Error("should contain DONE line")
	}
}

func TestWrapClaudePromptStructure(t *testing.T) {
	reqID := makeTestReqID(5)
	prompt := WrapClaudePrompt("do something", reqID)

	if !strings.Contains(prompt, ReqIDPrefix+" "+reqID) {
		t.Error("should contain REQ_ID line")
	}
	if !strings.Contains(prompt, BeginPrefix+" "+reqID) {
		t.Error("should contain BEGIN line")
	}
	if !strings.Contains(prompt, DonePrefix+" "+reqID) {
		t.Error("should contain DONE line")
	}
}

func TestExtractReplyStandardBasic(t *testing.T) {
	reqID := makeTestReqID(10)
	text := fmt.Sprintf("some preamble\nCURDX_DONE: %s\n", reqID)
	reply := ExtractReplyStandard(text, reqID, stubStripDone)
	if !strings.Contains(reply, "some preamble") {
		t.Errorf("expected 'some preamble' in reply, got %q", reply)
	}
}

func TestExtractReplyStandardEmptyOnWrongID(t *testing.T) {
	reqID := makeTestReqID(11)
	otherID := makeTestReqID(12)
	text := fmt.Sprintf("content\nCURDX_DONE: %s\n", otherID)
	reply := ExtractReplyStandard(text, reqID, stubStripDone)
	if reply != "" {
		t.Errorf("expected empty reply for wrong ID, got %q", reply)
	}
}

func TestExtractReplyStandardMultipleDoneMarkers(t *testing.T) {
	req1 := makeTestReqID(13)
	req2 := makeTestReqID(14)
	text := fmt.Sprintf("reply1\nCURDX_DONE: %s\nreply2\nCURDX_DONE: %s\n", req1, req2)
	reply := ExtractReplyStandard(text, req2, stubStripDone)
	if !strings.Contains(reply, "reply2") {
		t.Errorf("expected 'reply2' in reply, got %q", reply)
	}
	if strings.Contains(reply, "reply1") {
		t.Errorf("should not contain 'reply1', got %q", reply)
	}
}

func TestExtractReplyStandardNoMarkers(t *testing.T) {
	reqID := makeTestReqID(15)
	text := "just some plain text without markers"
	reply := ExtractReplyStandard(text, reqID, stubStripDone)
	if !strings.Contains(reply, "just some plain text") {
		t.Errorf("expected plain text, got %q", reply)
	}
}

func TestExtractReplyForClaudeWithBeginMarker(t *testing.T) {
	reqID := makeTestReqID(20)
	text := fmt.Sprintf("preamble\n%s %s\nthe reply\n%s %s\n",
		BeginPrefix, reqID, DonePrefix, reqID)
	reply := ExtractReplyForClaude(text, reqID, stubStripDone)
	if !strings.Contains(reply, "the reply") {
		t.Errorf("expected 'the reply', got %q", reply)
	}
	if strings.Contains(reply, "preamble") {
		t.Errorf("should not contain 'preamble', got %q", reply)
	}
}

func TestProviderRequestDefaults(t *testing.T) {
	req := ProviderRequest{
		ClientID: "client-1",
		WorkDir:  "/tmp/test",
		TimeoutS: 60.0,
		Quiet:    false,
		Message:  "hello",
	}
	if req.ClientID != "client-1" {
		t.Error("ClientID mismatch")
	}
	if req.OutputPath != "" {
		t.Error("OutputPath should be empty string by default")
	}
	if req.ReqID != "" {
		t.Error("ReqID should be empty string by default")
	}
}

func TestProviderResultDefaults(t *testing.T) {
	result := ProviderResult{
		ExitCode:   0,
		Reply:      "test reply",
		ReqID:      "abc123",
		SessionKey: "codex:xyz",
		DoneSeen:   true,
	}
	if result.ExitCode != 0 {
		t.Error("ExitCode mismatch")
	}
	if !result.DoneSeen {
		t.Error("DoneSeen should be true")
	}
	if result.AnchorSeen {
		t.Error("AnchorSeen should be false by default")
	}
	if result.FallbackScan {
		t.Error("FallbackScan should be false by default")
	}
	if result.DoneMs != nil {
		t.Error("DoneMs should be nil by default")
	}
}
