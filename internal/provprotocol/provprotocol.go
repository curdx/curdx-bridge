// Package provprotocol provides per-provider protocol helpers.
// Source: claude_code_bridge/lib/*askd_protocol.py
//
// Each provider has slightly different prompt wrapping but nearly identical
// reply extraction logic. This package consolidates them while preserving
// exact behavioral equivalence with each Python source.
package provprotocol

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Constants re-exported from protocol package for convenience.
const (
	ReqIDPrefix = "CCB_REQ_ID:"
	BeginPrefix = "CCB_BEGIN:"
	DonePrefix  = "CCB_DONE:"
)

// AnyDoneLineRe matches both old (32-char hex) and new (YYYYMMDD-HHMMSS-mmm-PID-counter) formats.
var AnyDoneLineRe = regexp.MustCompile(`(?i)^\s*CCB_DONE:\s*(?:[0-9a-f]{32}|\d{8}-\d{6}-\d{3}-\d+-\d+)\s*$`)

// protocolMarkerRe matches lines that look like CCB protocol markers (CCB_DONE:, CCB_REQ_ID:, CCB_BEGIN:).
var protocolMarkerRe = regexp.MustCompile(`(?im)^(\s*)(CCB_(?:DONE|REQ_ID|BEGIN):)`)

// sanitizeUserMessage prevents user-supplied content from injecting fake protocol markers.
// It prefixes any line matching a CCB protocol marker with a zero-width space to break parsing.
func sanitizeUserMessage(message string) string {
	return protocolMarkerRe.ReplaceAllString(message, "${1}\u200B${2}")
}

// ── Codex (caskd_protocol.py) ──
// Pure shim — re-exports from ccb_protocol. All functions are in protocol package.

// ── Gemini (gaskd_protocol.py) ──

// WrapGeminiPrompt wraps a prompt with CCB markers for Gemini.
func WrapGeminiPrompt(message, reqID string) string {
	message = sanitizeUserMessage(strings.TrimRight(message, " \t\n\r"))
	return fmt.Sprintf(
		"%s %s\n\n"+
			"%s\n\n"+
			"IMPORTANT — you MUST follow these rules:\n"+
			"1. Reply in English with an execution summary. Do not stay silent.\n"+
			"2. Your FINAL line MUST be exactly (copy verbatim, no extra text):\n"+
			"   %s %s\n"+
			"3. Do NOT omit, modify, or paraphrase the line above.\n",
		ReqIDPrefix, reqID,
		message,
		DonePrefix, reqID,
	)
}

// ── OpenCode (oaskd_protocol.py) ──

// WrapOpenCodePrompt wraps a prompt with CCB markers for OpenCode.
func WrapOpenCodePrompt(message, reqID string) string {
	message = sanitizeUserMessage(strings.TrimRight(message, " \t\n\r"))
	return fmt.Sprintf(
		"%s %s\n\n"+
			"%s\n\n"+
			"IMPORTANT:\n"+
			"- Reply normally, in English.\n"+
			"- End your reply with this exact final line (verbatim, on its own line):\n"+
			"%s %s\n",
		ReqIDPrefix, reqID,
		message,
		DonePrefix, reqID,
	)
}

// ── Claude (laskd_protocol.py) ──

var (
	claudeSkillCache *string
	claudeSkillOnce  sync.Once
)

func loadClaudeSkills() string {
	claudeSkillOnce.Do(func() {
		s := ""
		claudeSkillCache = &s

		if !envBool("CCB_CLAUDE_SKILLS", true) {
			return
		}

		exe, err := os.Executable()
		if err != nil {
			return
		}
		skillsDir := filepath.Join(filepath.Dir(exe), "..", "claude_skills")
		if info, err := os.Stat(skillsDir); err != nil || !info.IsDir() {
			return
		}

		var parts []string
		for _, name := range []string{"ask.md"} {
			path := filepath.Join(skillsDir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			text := strings.TrimSpace(string(data))
			if text != "" {
				parts = append(parts, text)
			}
		}
		result := strings.TrimSpace(strings.Join(parts, "\n\n"))
		claudeSkillCache = &result
	})
	return *claudeSkillCache
}

func wantsMarkdownTable(message string) bool {
	lower := strings.ToLower(message)
	if !strings.Contains(lower, "markdown") {
		return false
	}
	return strings.Contains(lower, "table") || strings.Contains(message, "表格")
}

func languageHint() string {
	lang := strings.TrimSpace(strings.ToLower(
		coalesce(os.Getenv("CCB_REPLY_LANG"), os.Getenv("CCB_LANG")),
	))
	switch lang {
	case "zh", "cn", "chinese":
		return "Reply in Chinese."
	case "en", "english":
		return "Reply in English."
	default:
		return ""
	}
}

// WrapClaudePrompt wraps a prompt with CCB markers for Claude (local).
func WrapClaudePrompt(message, reqID string) string {
	message = sanitizeUserMessage(strings.TrimRight(message, " \t\n\r"))
	skills := loadClaudeSkills()
	if skills != "" {
		message = strings.TrimSpace(skills + "\n\n" + message)
	}

	var extraLines []string
	if wantsMarkdownTable(message) {
		extraLines = append(extraLines, "If asked for a Markdown table, output only pipe-and-dash Markdown table syntax (no box-drawing characters).")
	}
	if hint := languageHint(); hint != "" {
		extraLines = append(extraLines, hint)
	}
	extra := strings.TrimSpace(strings.Join(extraLines, "\n"))
	if extra != "" {
		extra = extra + "\n\n"
	}

	return fmt.Sprintf(
		"%s %s\n\n"+
			"%s\n\n"+
			"%s"+
			"Reply using exactly this format:\n"+
			"%s %s\n"+
			"<reply>\n"+
			"%s %s\n",
		ReqIDPrefix, reqID,
		message,
		extra,
		BeginPrefix, reqID,
		DonePrefix, reqID,
	)
}

// ── Common reply extraction ──

// ExtractReplyStandard extracts the reply segment for reqID using the standard
// algorithm shared by gemini and opencode.
// This is the common pattern from *askd_protocol.py.
func ExtractReplyStandard(text, reqID string, stripDone func(string, string) string) string {
	lines := splitLines(text)
	if len(lines) == 0 {
		return ""
	}

	targetRe := regexp.MustCompile(`(?i)^\s*CCB_DONE:\s*` + regexp.QuoteMeta(reqID) + `\s*$`)
	var doneIdxs []int
	var targetIdxs []int
	for i, ln := range lines {
		if AnyDoneLineRe.MatchString(ln) {
			doneIdxs = append(doneIdxs, i)
			if targetRe.MatchString(ln) {
				targetIdxs = append(targetIdxs, i)
			}
		}
	}

	if len(targetIdxs) == 0 {
		if len(doneIdxs) > 0 {
			return "" // Prevent returning old content
		}
		return stripDone(text, reqID)
	}

	targetI := targetIdxs[len(targetIdxs)-1]
	prevDoneI := -1
	for j := len(doneIdxs) - 1; j >= 0; j-- {
		if doneIdxs[j] < targetI {
			prevDoneI = doneIdxs[j]
			break
		}
	}

	segment := lines[prevDoneI+1 : targetI]
	segment = trimBlankLines(segment)
	return strings.TrimRight(strings.Join(segment, "\n"), " \t\n\r")
}

// ExtractReplyForClaude extracts the reply segment for reqID from a Claude message.
// Claude uses BEGIN markers in addition to DONE markers.
// Source: laskd_protocol.py:extract_reply_for_req
func ExtractReplyForClaude(text, reqID string, stripDone func(string, string) string) string {
	lines := splitLines(text)
	if len(lines) == 0 {
		return ""
	}

	targetRe := regexp.MustCompile(`(?i)^\s*CCB_DONE:\s*` + regexp.QuoteMeta(reqID) + `\s*$`)
	beginRe := regexp.MustCompile(`(?i)^\s*` + regexp.QuoteMeta(BeginPrefix) + `\s*` + regexp.QuoteMeta(reqID) + `\s*$`)

	var doneIdxs []int
	var targetIdxs []int
	for i, ln := range lines {
		if AnyDoneLineRe.MatchString(ln) {
			doneIdxs = append(doneIdxs, i)
			if targetRe.MatchString(ln) {
				targetIdxs = append(targetIdxs, i)
			}
		}
	}

	if len(targetIdxs) == 0 {
		return stripDone(text, reqID)
	}

	targetI := targetIdxs[len(targetIdxs)-1]

	// Look for BEGIN marker
	beginI := -1
	for i := targetI - 1; i >= 0; i-- {
		if beginRe.MatchString(lines[i]) {
			beginI = i
			break
		}
	}

	var segment []string
	if beginI >= 0 {
		segment = lines[beginI+1 : targetI]
	} else {
		prevDoneI := -1
		for j := len(doneIdxs) - 1; j >= 0; j-- {
			if doneIdxs[j] < targetI {
				prevDoneI = doneIdxs[j]
				break
			}
		}
		segment = lines[prevDoneI+1 : targetI]
	}

	segment = trimBlankLines(segment)
	return strings.TrimRight(strings.Join(segment, "\n"), " \t\n\r")
}

// ── Request/Result types ──

// ProviderRequest is a unified request type for all providers.
// Source: *askd_protocol.py Request dataclasses
type ProviderRequest struct {
	ClientID   string
	WorkDir    string
	TimeoutS   float64
	Quiet      bool
	Message    string
	OutputPath string // optional
	ReqID      string // optional
	Caller     string // default varies by provider
	NoWrap     bool   // Claude only
}

// ProviderResult is a unified result type for all providers.
// Source: *askd_protocol.py Result dataclasses
type ProviderResult struct {
	ExitCode     int
	Reply        string
	ReqID        string
	SessionKey   string
	DoneSeen     bool
	DoneMs       *int // nil = not set
	AnchorSeen   bool
	FallbackScan bool
	AnchorMs     *int // nil = not set
	LogPath      string
}

// ── Helpers ──

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	raw := strings.Split(text, "\n")
	result := make([]string, len(raw))
	for i, ln := range raw {
		result[i] = strings.TrimRight(ln, "\n")
	}
	return result
}

func trimBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func envBool(name string, defaultVal bool) bool {
	raw, ok := os.LookupEnv(name)
	if !ok || raw == "" {
		return defaultVal
	}
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "0", "false", "no", "off":
		return false
	case "1", "true", "yes", "on":
		return true
	default:
		return defaultVal
	}
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
