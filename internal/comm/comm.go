// Package comm implements communication with provider CLI sessions.
//
// Each provider has a LogReader (reads replies from session files) and a
// Communicator (loads session info, checks pane health, sends text).
//
// Source: claude_code_bridge/lib/*_comm.py
package comm

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// ANSI escape stripping (shared by pane-log readers)
// ---------------------------------------------------------------------------

// ansiEscapeRE matches ANSI/VT escape sequences:
//   - CSI sequences: ESC [ ... final byte
//   - OSC sequences: ESC ] ... BEL or ST
//   - Fe sequences: ESC + 2-byte
var ansiEscapeRE = regexp.MustCompile(
	"\x1b(?:" +
		`\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]` + // CSI
		`|\].*?(?:\x07|\x1b\\)` + // OSC
		`|[\x40-\x5f]` + // Fe
		")",
)

// StripANSI removes ANSI/VT escape sequences from raw terminal output.
func StripANSI(text string) string {
	return ansiEscapeRE.ReplaceAllString(text, "")
}

// ---------------------------------------------------------------------------
// CCB marker patterns (shared by pane-log readers)
// ---------------------------------------------------------------------------

// ccbReqIDRE matches CCB_REQ_ID: <id> lines.
var ccbReqIDRE = regexp.MustCompile(`(?m)^\s*CCB_REQ_ID:\s*(\S+)\s*$`)

// ccbDoneRE matches CCB_DONE: <id> lines (both old hex and new timestamp formats).
var ccbDoneRE = regexp.MustCompile(
	`(?mi)^\s*CCB_DONE:\s*(?:[0-9a-f]{32}|\d{8}-\d{6}-\d{3}-\d+-\d+)\s*$`,
)

// ---------------------------------------------------------------------------
// PaneLogState tracks the read offset into a pane log file.
// ---------------------------------------------------------------------------

// PaneLogState is the cursor into a pane-log file for incremental reading.
type PaneLogState struct {
	PaneLogPath string
	Offset      int64
}

// ---------------------------------------------------------------------------
// Event is a (role, text) pair from a conversation stream.
// ---------------------------------------------------------------------------

// Event represents a single conversation event extracted from logs.
type Event struct {
	Role string // "user" or "assistant"
	Text string
}

// ConvPair is a (user_prompt, assistant_reply) pair.
type ConvPair struct {
	User      string
	Assistant string
}

// ---------------------------------------------------------------------------
// Shared pane-log extraction helpers
// ---------------------------------------------------------------------------

// extractAssistantBlocks extracts assistant reply blocks from cleaned terminal text.
// A reply block is text between a CCB_REQ_ID marker and the corresponding CCB_DONE marker.
func extractAssistantBlocks(text string) []string {
	type reqPos struct {
		end int
		id  string
	}
	reqMatches := ccbReqIDRE.FindAllStringSubmatchIndex(text, -1)
	var reqPositions []reqPos
	for _, m := range reqMatches {
		reqPositions = append(reqPositions, reqPos{end: m[1], id: text[m[2]:m[3]]})
	}

	doneMatches := ccbDoneRE.FindAllStringIndex(text, -1)
	var doneStarts []int
	for _, m := range doneMatches {
		doneStarts = append(doneStarts, m[0])
	}

	if len(reqPositions) == 0 && len(doneStarts) == 0 {
		stripped := strings.TrimSpace(text)
		if stripped != "" {
			return []string{stripped}
		}
		return nil
	}

	var blocks []string
	for _, rp := range reqPositions {
		nextDone := -1
		for _, dp := range doneStarts {
			if dp > rp.end {
				nextDone = dp
				break
			}
		}
		if nextDone >= 0 {
			segment := strings.TrimSpace(text[rp.end:nextDone])
			if segment != "" {
				blocks = append(blocks, segment)
			}
		} else {
			segment := strings.TrimSpace(text[rp.end:])
			if segment != "" {
				blocks = append(blocks, segment)
			}
		}
	}
	return blocks
}

// extractConversationPairs extracts (user_prompt, assistant_reply) pairs from terminal text.
func extractConversationPairs(text string) []ConvPair {
	reqMatches := ccbReqIDRE.FindAllStringIndex(text, -1)
	doneMatches := ccbDoneRE.FindAllStringIndex(text, -1)
	var doneStarts []int
	for _, m := range doneMatches {
		doneStarts = append(doneStarts, m[0])
	}

	var pairs []ConvPair
	prevEnd := 0
	for _, rm := range reqMatches {
		userText := strings.TrimSpace(text[prevEnd:rm[0]])
		reqEnd := rm[1]

		nextDone := -1
		for _, dp := range doneStarts {
			if dp > reqEnd {
				nextDone = dp
				break
			}
		}

		var assistantText string
		if nextDone >= 0 {
			assistantText = strings.TrimSpace(text[reqEnd:nextDone])
			prevEnd = nextDone
		} else {
			assistantText = strings.TrimSpace(text[reqEnd:])
			prevEnd = len(text)
		}
		pairs = append(pairs, ConvPair{User: userText, Assistant: assistantText})
	}
	return pairs
}

// ---------------------------------------------------------------------------
// Shared file-reading helpers
// ---------------------------------------------------------------------------

// readNewPaneContent reads new bytes from a pane log starting at offset,
// strips ANSI, and returns assistant blocks.
func readNewPaneContent(logPath string, offset int64) (latest string, newOffset int64, ok bool) {
	fi, err := os.Stat(logPath)
	if err != nil {
		return "", offset, false
	}
	size := fi.Size()
	if size < offset {
		offset = 0
	}
	if size == offset {
		return "", offset, false
	}

	f, err := os.Open(logPath)
	if err != nil {
		return "", offset, false
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", offset, false
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", offset, false
	}
	newOffset = offset + int64(len(data))
	clean := StripANSI(string(data))
	blocks := extractAssistantBlocks(clean)
	if len(blocks) > 0 {
		return blocks[len(blocks)-1], newOffset, true
	}
	return "", newOffset, false
}

// readNewPaneEvents reads new bytes from a pane log starting at offset,
// strips ANSI, and returns conversation events.
func readNewPaneEvents(logPath string, offset int64) (events []Event, newOffset int64) {
	fi, err := os.Stat(logPath)
	if err != nil {
		return nil, offset
	}
	size := fi.Size()
	if size < offset {
		offset = 0
	}
	if size == offset {
		return nil, offset
	}

	f, err := os.Open(logPath)
	if err != nil {
		return nil, offset
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, offset
	}
	newOffset = offset + int64(len(data))
	clean := StripANSI(string(data))

	pairs := extractConversationPairs(clean)
	for _, p := range pairs {
		if p.User != "" {
			events = append(events, Event{Role: "user", Text: p.User})
		}
		if p.Assistant != "" {
			events = append(events, Event{Role: "assistant", Text: p.Assistant})
		}
	}
	return events, newOffset
}

// ---------------------------------------------------------------------------
// Environment helpers
// ---------------------------------------------------------------------------

func envFloat(name string, defaultVal float64) float64 {
	raw := os.Getenv(name)
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return defaultVal
	}
	return v
}

func envInt(name string, defaultVal int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return defaultVal
	}
	return v
}

func envBool(name string) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}

func clampPollInterval(v, min_, max_ float64) float64 {
	if v < min_ {
		return min_
	}
	if v > max_ {
		return max_
	}
	return v
}

// pollSleep sleeps for the given duration. Extracted to allow tests to mock.
func pollSleep(d time.Duration) {
	time.Sleep(d)
}

// newLineScanner returns a bufio.Scanner that can handle lines up to 1MB.
func newLineScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return s
}
