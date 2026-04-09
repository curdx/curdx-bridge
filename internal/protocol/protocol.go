// Package protocol implements the CCB request/response framing protocol.
// Source: claude_code_bridge/lib/ccb_protocol.py
package protocol

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

const (
	REQIDPrefix  = "CCB_REQ_ID:"
	BeginPrefix  = "CCB_BEGIN:"
	DonePrefix   = "CCB_DONE:"
)

const doneLineRETemplate = `^\s*CCB_DONE:\s*%s\s*$`

// trailingDoneTagRE matches lines like "HARNESS_DONE" or "FOO_DONE: 20260125-..."
// but NOT "CCB_DONE: ...". Go's RE2 lacks negative lookahead (?!), so we use
// a two-step check: match the general pattern, then exclude CCB_DONE.
var trailingDoneTagRE = regexp.MustCompile(
	`^\s*[A-Z][A-Z0-9_]*_DONE(?:\s*:\s*\d{8}-\d{6}-\d{3}-\d+-\d+)?\s*$`,
)
var ccbDonePrefixRE = regexp.MustCompile(`^\s*CCB_DONE\s*:`)

var anyCCBDoneLineRE = regexp.MustCompile(
	`^\s*CCB_DONE:\s*\d{8}-\d{6}-\d{3}-\d+-\d+\s*$`,
)

// isTrailingDoneTag replicates the Python _TRAILING_DONE_TAG_RE which uses
// a negative lookahead to exclude CCB_DONE lines.
func isTrailingDoneTag(line string) bool {
	return trailingDoneTagRE.MatchString(line) && !ccbDonePrefixRE.MatchString(line)
}

// isTrailingNoiseLine returns true for blank lines and generic harness
// completion tags that should be ignored when scanning for protocol markers.
func isTrailingNoiseLine(line string) bool {
	if strings.TrimSpace(line) == "" {
		return true
	}
	return isTrailingDoneTag(line)
}

// StripTrailingMarkers removes trailing protocol/harness marker lines
// (blank lines, CCB_DONE: <id>, and other *_DONE tags).
// This is meant for "recall"/display commands (e.g. cpend) where we want a clean view.
func StripTrailingMarkers(text string) string {
	lines := splitAndRstrip(text)
	for len(lines) > 0 {
		last := lines[len(lines)-1]
		if isTrailingNoiseLine(last) || anyCCBDoneLineRE.MatchString(last) {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\r\n")
}

// reqIDCounter is the global atomic counter for MakeReqID.
var reqIDCounter int64

// MakeReqID generates a unique request ID in the format:
// YYYYMMDD-HHMMSS-mmm-PID-counter
func MakeReqID() string {
	now := time.Now()
	ms := now.Nanosecond() / 1_000_000
	cnt := atomic.AddInt64(&reqIDCounter, 1)
	return fmt.Sprintf("%s-%03d-%d-%d",
		now.Format("20060102-150405"),
		ms,
		os.Getpid(),
		cnt,
	)
}

// WrapCodexPrompt wraps a user message with CCB protocol framing for Codex.
func WrapCodexPrompt(message, reqID string) string {
	message = strings.TrimRight(message, " \t\r\n")
	return fmt.Sprintf("%s %s\n\n%s\n\nIMPORTANT:\n- Reply normally.\n- Reply normally, in English.\n- End your reply with this exact final line (verbatim, on its own line):\n%s %s\n",
		REQIDPrefix, reqID,
		message,
		DonePrefix, reqID,
	)
}

// DoneLineRE returns a compiled regex matching the done line for the given request ID.
func DoneLineRE(reqID string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(doneLineRETemplate, regexp.QuoteMeta(reqID)))
}

// IsDoneText checks whether the given text contains the CCB_DONE line for reqID
// as the last meaningful (non-noise) line.
func IsDoneText(text, reqID string) bool {
	lines := splitAndRstrip(text)
	re := DoneLineRE(reqID)
	for i := len(lines) - 1; i >= 0; i-- {
		if isTrailingNoiseLine(lines[i]) {
			continue
		}
		return re.MatchString(lines[i])
	}
	return false
}

// StripDoneText strips the CCB_DONE line (and surrounding noise) from text.
func StripDoneText(text, reqID string) string {
	lines := splitAndRstrip(text)
	if len(lines) == 0 {
		return ""
	}

	// Trim trailing noise lines
	for len(lines) > 0 && isTrailingNoiseLine(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}

	// Remove the done line if it matches
	re := DoneLineRE(reqID)
	if len(lines) > 0 && re.MatchString(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}

	// Trim trailing noise again
	for len(lines) > 0 && isTrailingNoiseLine(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}

	return strings.TrimRight(strings.Join(lines, "\n"), " \t\r\n")
}

// ExtractReplyForReq extracts the reply segment for reqID from a message.
// When multiple replies are present (each ending with CCB_DONE: <req_id>),
// extract only the segment between the previous done line and the done line
// for our req_id.
func ExtractReplyForReq(text, reqID string) string {
	lines := splitAndRstrip(text)
	if len(lines) == 0 {
		return ""
	}

	targetRE := regexp.MustCompile(`(?i)^\s*CCB_DONE:\s*` + regexp.QuoteMeta(reqID) + `\s*$`)

	var doneIdxs []int
	var targetIdxs []int
	for i, ln := range lines {
		if anyCCBDoneLineRE.MatchString(ln) {
			doneIdxs = append(doneIdxs, i)
			if targetRE.MatchString(ln) {
				targetIdxs = append(targetIdxs, i)
			}
		}
	}

	if len(targetIdxs) == 0 {
		// No CCB_DONE for our req_id found
		if len(doneIdxs) > 0 {
			return "" // Prevent returning old content
		}
		// No CCB_DONE markers at all - fallback to strip behavior
		return StripDoneText(text, reqID)
	}

	// Find the last occurrence of our req_id's done line
	targetI := targetIdxs[len(targetIdxs)-1]

	// Find the previous done line (any req_id)
	prevDoneI := -1
	for j := len(doneIdxs) - 1; j >= 0; j-- {
		if doneIdxs[j] < targetI {
			prevDoneI = doneIdxs[j]
			break
		}
	}

	// Extract segment between previous done and our done
	segment := lines[prevDoneI+1 : targetI]

	// Trim leading blank lines
	for len(segment) > 0 && strings.TrimSpace(segment[0]) == "" {
		segment = segment[1:]
	}
	// Trim trailing blank lines
	for len(segment) > 0 && strings.TrimSpace(segment[len(segment)-1]) == "" {
		segment = segment[:len(segment)-1]
	}

	return strings.TrimRight(strings.Join(segment, "\n"), " \t\r\n")
}

// CaskdRequest mirrors the Python CaskdRequest dataclass.
type CaskdRequest struct {
	ClientID   string
	WorkDir    string
	TimeoutS   float64
	Quiet      bool
	Message    string
	OutputPath string // empty string means nil/None
	ReqID      string // empty string means nil/None
	Caller     string // defaults to "claude"
}

// CaskdResult mirrors the Python CaskdResult dataclass.
type CaskdResult struct {
	ExitCode     int
	Reply        string
	ReqID        string
	SessionKey   string
	LogPath      string // empty string means nil/None
	AnchorSeen   bool
	DoneSeen     bool
	FallbackScan bool
	AnchorMs     *int // nil means None
	DoneMs       *int // nil means None
}

// splitAndRstrip splits text into lines, stripping \n from each line
// (equivalent to Python: [ln.rstrip("\n") for ln in (text or "").splitlines()])
func splitAndRstrip(text string) []string {
	if text == "" {
		return nil
	}
	// Normalize \r\n to \n before splitting.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	raw := strings.Split(text, "\n")
	// Remove trailing empty element from trailing newline
	// Python's splitlines() does not produce trailing empty string
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	result := make([]string, len(raw))
	for i, ln := range raw {
		result[i] = strings.TrimRight(ln, "\n")
	}
	return result
}
