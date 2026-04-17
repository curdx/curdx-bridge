// Package protocol - tests for CURDX protocol functions.
// Ported from: claude_code_bridge/test/test_curdx_protocol.py
package protocol

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestMakeReqIDFormatAndUniqueness(t *testing.T) {
	const n = 2000
	ids := make([]string, n)
	seen := make(map[string]struct{}, n)
	re := regexp.MustCompile(`^\d{8}-\d{6}-\d{3}-\d+-\d+$`)

	for i := range n {
		ids[i] = MakeReqID()
	}

	for _, rid := range ids {
		if _, dup := seen[rid]; dup {
			t.Fatalf("duplicate req_id: %s", rid)
		}
		seen[rid] = struct{}{}
		if !re.MatchString(rid) {
			t.Fatalf("req_id %q does not match expected format YYYYMMDD-HHMMSS-mmm-PID-counter", rid)
		}
	}
}

func TestWrapCodexPromptStructure(t *testing.T) {
	reqID := MakeReqID()
	message := "hello\nworld"
	prompt := WrapCodexPrompt(message, reqID)

	expected := fmt.Sprintf("%s %s", REQIDPrefix, reqID)
	if !strings.Contains(prompt, expected) {
		t.Fatalf("prompt missing REQ_ID_PREFIX line: %q", prompt)
	}
	if !strings.Contains(prompt, "IMPORTANT:") {
		t.Fatal("prompt missing IMPORTANT:")
	}
	if !strings.Contains(prompt, "- Reply normally.") {
		t.Fatal("prompt missing '- Reply normally.'")
	}
	doneLine := fmt.Sprintf("%s %s", DonePrefix, reqID)
	if !strings.Contains(prompt, doneLine) {
		t.Fatalf("prompt missing DONE_PREFIX line: %q", prompt)
	}
	if !strings.HasSuffix(prompt, doneLine+"\n") {
		t.Fatalf("prompt should end with DONE_PREFIX line followed by newline, got: %q", prompt)
	}
}

func TestIsDoneTextRecognizesLastNonemptyLine(t *testing.T) {
	reqID := MakeReqID()

	ok := fmt.Sprintf("hi\n%s %s\n", DonePrefix, reqID)
	if !IsDoneText(ok, reqID) {
		t.Fatal("expected IsDoneText to be true for basic done text")
	}

	okWithTrailingBlanks := fmt.Sprintf("hi\n%s %s\n\n\n", DonePrefix, reqID)
	if !IsDoneText(okWithTrailingBlanks, reqID) {
		t.Fatal("expected IsDoneText to be true with trailing blanks")
	}

	okWithTrailingHarnessDone := fmt.Sprintf("hi\n%s %s\nHARNESS_DONE\n", DonePrefix, reqID)
	if !IsDoneText(okWithTrailingHarnessDone, reqID) {
		t.Fatal("expected IsDoneText to be true with trailing HARNESS_DONE")
	}

	okWithTrailingHarnessDoneAndBlanks := fmt.Sprintf("hi\n%s %s\n\nHARNESS_DONE\n\n", DonePrefix, reqID)
	if !IsDoneText(okWithTrailingHarnessDoneAndBlanks, reqID) {
		t.Fatal("expected IsDoneText to be true with trailing HARNESS_DONE and blanks")
	}

	notLast := fmt.Sprintf("%s %s\nhi\n", DonePrefix, reqID)
	if IsDoneText(notLast, reqID) {
		t.Fatal("expected IsDoneText to be false when done line is not last")
	}

	otherID := MakeReqID()
	wrongID := fmt.Sprintf("hi\n%s %s\n", DonePrefix, otherID)
	if IsDoneText(wrongID, reqID) {
		t.Fatal("expected IsDoneText to be false for wrong req_id")
	}

	onlyHarnessDone := "hi\nHARNESS_DONE\n"
	if IsDoneText(onlyHarnessDone, reqID) {
		t.Fatal("expected IsDoneText to be false for only HARNESS_DONE")
	}
}

func TestStripDoneTextRemovesDoneLine(t *testing.T) {
	reqID := MakeReqID()

	text := fmt.Sprintf("line1\nline2\n%s %s\n\n", DonePrefix, reqID)
	result := StripDoneText(text, reqID)
	if result != "line1\nline2" {
		t.Fatalf("expected 'line1\\nline2', got %q", result)
	}

	textWithHarnessDone := fmt.Sprintf("line1\nline2\n%s %s\nHARNESS_DONE\n", DonePrefix, reqID)
	result = StripDoneText(textWithHarnessDone, reqID)
	if result != "line1\nline2" {
		t.Fatalf("expected 'line1\\nline2', got %q", result)
	}
}

func TestStripTrailingMarkersRemovesDoneAndHarnessTrailers(t *testing.T) {
	reqID := MakeReqID()

	text := fmt.Sprintf("line1\nline2\n%s %s\nHARNESS_DONE\n\n", DonePrefix, reqID)
	result := StripTrailingMarkers(text)
	if result != "line1\nline2" {
		t.Fatalf("expected 'line1\\nline2', got %q", result)
	}
}
