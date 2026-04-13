// Package memory - transfer.go provides context transfer orchestration.
// Source: claude_code_bridge/lib/memory/transfer.py
//
// Coordinates the full pipeline: parse -> dedupe -> truncate -> format -> send.
package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/curdx/curdx-bridge/internal/sessionutil"
)

// ContextTransfer orchestrates context transfer between providers.
type ContextTransfer struct {
	MaxTokens int
	WorkDir   string
	Parser    *ClaudeSessionParser
	Deduper   *ConversationDeduper
	Formatter *ContextFormatter
}

// Supported providers and sources.
var (
	SupportedProviders    = []string{"codex", "gemini", "opencode"}
	SupportedSources      = []string{"auto", "claude", "codex", "gemini", "opencode"}
	SourceSessionFiles    = map[string]string{
		"claude":   ".claude-session",
		"codex":    ".codex-session",
		"gemini":   ".gemini-session",
		"opencode": ".opencode-session",
	}
	DefaultSourceOrder    = []string{"claude", "codex", "gemini", "opencode"}
	DefaultFallbackPairs  = 50
)

// Provider command map for send_to_provider.
var providerCmdMap = map[string]string{
	"codex":    "cxb-codex-ask",
	"gemini":   "cxb-gemini-ask",
	"opencode": "cxb-opencode-ask",
}

// NewContextTransfer creates a new ContextTransfer.
func NewContextTransfer(maxTokens int, workDir string) *ContextTransfer {
	if maxTokens <= 0 {
		maxTokens = 8000
	}
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	return &ContextTransfer{
		MaxTokens: maxTokens,
		WorkDir:   workDir,
		Parser:    NewClaudeSessionParser(""),
		Deduper:   &ConversationDeduper{},
		Formatter: NewContextFormatter(maxTokens),
	}
}

func (ct *ContextTransfer) normalizeProvider(provider string) string {
	v := strings.TrimSpace(strings.ToLower(provider))
	if v == "" {
		return "auto"
	}
	return v
}

func (ct *ContextTransfer) loadSessionData(provider string) (string, map[string]interface{}) {
	filename, ok := SourceSessionFiles[provider]
	if !ok {
		return "", nil
	}
	sessionFile := sessionutil.FindProjectSessionFile(ct.WorkDir, filename)
	if sessionFile == "" {
		return "", nil
	}
	if _, err := os.Stat(sessionFile); err != nil {
		return "", nil
	}
	raw, err := os.ReadFile(sessionFile)
	if err != nil {
		return "", nil
	}
	// Strip UTF-8 BOM
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		raw = raw[3:]
	}
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return sessionFile, nil
	}
	if data == nil {
		return sessionFile, nil
	}
	return sessionFile, data
}

func (ct *ContextTransfer) autoSourceCandidates() []string {
	type candidate struct {
		mtime    float64
		provider string
	}
	var candidates []candidate
	for _, provider := range DefaultSourceOrder {
		filename, ok := SourceSessionFiles[provider]
		if !ok {
			continue
		}
		sessionFile := sessionutil.FindProjectSessionFile(ct.WorkDir, filename)
		if sessionFile == "" {
			continue
		}
		info, err := os.Stat(sessionFile)
		if err != nil {
			continue
		}
		mtime := float64(info.ModTime().UnixMilli()) / 1000.0
		candidates = append(candidates, candidate{mtime, provider})
	}

	// Sort by mtime descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].mtime > candidates[j].mtime
	})

	ordered := make([]string, 0, len(DefaultSourceOrder))
	seen := map[string]bool{}
	for _, c := range candidates {
		ordered = append(ordered, c.provider)
		seen[c.provider] = true
	}
	for _, p := range DefaultSourceOrder {
		if !seen[p] {
			ordered = append(ordered, p)
		}
	}
	return ordered
}

func (ct *ContextTransfer) defaultFetchN() int {
	return DefaultFallbackPairs
}

func (ct *ContextTransfer) contextFromPairs(
	pairs [][2]string,
	provider string,
	sessionID string,
	sessionPath string,
	lastN int,
	stats *SessionStats,
) *TransferContext {
	var cleanedPairs [][2]string
	var prevHash string

	for _, pair := range pairs {
		cleanedUser := ct.Deduper.CleanContent(pair[0])
		cleanedAssistant := ct.Deduper.CleanContent(pair[1])
		if cleanedUser == "" && cleanedAssistant == "" {
			continue
		}
		pairHash := fmt.Sprintf("%d::%d", hash(cleanedUser), hash(cleanedAssistant))
		if pairHash == prevHash {
			continue
		}
		cleanedPairs = append(cleanedPairs, [2]string{cleanedUser, cleanedAssistant})
		prevHash = pairHash
	}

	if lastN > 0 && len(cleanedPairs) > lastN {
		cleanedPairs = cleanedPairs[len(cleanedPairs)-lastN:]
	}

	cleanedPairs = ct.Formatter.TruncateToLimit(cleanedPairs, ct.MaxTokens)

	totalText := ""
	for _, pair := range cleanedPairs {
		totalText += pair[0] + pair[1]
	}
	tokenEstimate := ct.Formatter.EstimateTokens(totalText)

	metadata := map[string]any{"provider": provider}
	if sessionPath != "" {
		metadata["session_path"] = sessionPath
	}

	return &TransferContext{
		Conversations:   cleanedPairs,
		SourceSessionID: sessionID,
		TokenEstimate:   tokenEstimate,
		Metadata:        metadata,
		Stats:           stats,
		SourceProvider:  provider,
	}
}

// hash computes a simple integer hash for deduplication (mirrors Python hash()).
func hash(s string) uint64 {
	var h uint64 = 5381
	for _, c := range s {
		h = ((h << 5) + h) + uint64(c)
	}
	return h
}

// cleanEntries cleans all conversation entries.
func (ct *ContextTransfer) cleanEntries(entries []ConversationEntry) []ConversationEntry {
	var result []ConversationEntry
	for _, entry := range entries {
		cleaned := ct.Deduper.CleanContent(entry.Content)
		if cleaned != "" || len(entry.ToolCalls) > 0 {
			result = append(result, ConversationEntry{
				Role:       entry.Role,
				Content:    cleaned,
				UUID:       entry.UUID,
				ParentUUID: entry.ParentUUID,
				Timestamp:  entry.Timestamp,
				ToolCalls:  entry.ToolCalls,
			})
		}
	}
	return result
}

// buildPairs builds user/assistant conversation pairs.
func (ct *ContextTransfer) buildPairs(entries []ConversationEntry) [][2]string {
	var pairs [][2]string
	var currentUser *string

	for _, entry := range entries {
		if entry.Role == "user" {
			s := entry.Content
			currentUser = &s
		} else if entry.Role == "assistant" && currentUser != nil {
			pairs = append(pairs, [2]string{*currentUser, entry.Content})
			currentUser = nil
		}
	}
	return pairs
}

// ExtractConversations extracts and processes conversations from a session.
func (ct *ContextTransfer) ExtractConversations(
	sessionPath string,
	lastN int,
	includeStats bool,
	sourceProvider string,
	sourceSessionID string,
	sourceProjectID string,
) (*TransferContext, error) {
	provider := ct.normalizeProvider(sourceProvider)

	if provider == "auto" {
		if sessionPath != "" {
			return ct.extractFromClaude(sessionPath, lastN, includeStats)
		}
		var lastError error
		for _, candidate := range ct.autoSourceCandidates() {
			ctx, err := ct.extractByProvider(candidate, sessionPath, lastN, includeStats, sourceSessionID, sourceProjectID)
			if err != nil {
				if _, ok := err.(*SessionNotFoundError); ok {
					lastError = err
					continue
				}
				return nil, err
			}
			return ctx, nil
		}
		if lastError != nil {
			return nil, lastError
		}
		return nil, &SessionNotFoundError{Msg: "No sessions found for any provider"}
	}

	return ct.extractByProvider(provider, sessionPath, lastN, includeStats, sourceSessionID, sourceProjectID)
}

func (ct *ContextTransfer) extractByProvider(
	provider string,
	sessionPath string,
	lastN int,
	includeStats bool,
	sourceSessionID string,
	sourceProjectID string,
) (*TransferContext, error) {
	switch provider {
	case "claude":
		return ct.extractFromClaude(sessionPath, lastN, includeStats)
	case "codex":
		return ct.extractFromGeneric("codex", sessionPath, lastN, sourceSessionID)
	case "gemini":
		return ct.extractFromGeneric("gemini", sessionPath, lastN, sourceSessionID)
	case "opencode":
		return ct.extractFromGeneric("opencode", sessionPath, lastN, sourceSessionID)
	default:
		return nil, &SessionNotFoundError{Msg: fmt.Sprintf("Unsupported source provider: %s", provider)}
	}
}

func (ct *ContextTransfer) extractFromClaude(
	sessionPath string,
	lastN int,
	includeStats bool,
) (*TransferContext, error) {
	resolved, err := ct.Parser.ResolveSession(ct.WorkDir, sessionPath)
	if err != nil {
		return nil, err
	}

	info := ct.Parser.GetSessionInfo(resolved)
	info.Provider = "claude"

	var stats *SessionStats
	if includeStats {
		stats = ct.Parser.ExtractSessionStats(resolved)
	}

	entries, err := ct.Parser.ParseSession(resolved)
	if err != nil {
		return nil, err
	}

	entries = ct.cleanEntries(entries)
	entries = ct.Deduper.DedupeMessages(entries)
	entries = ct.Deduper.CollapseToolCalls(entries)

	pairs := ct.buildPairs(entries)
	if lastN > 0 && len(pairs) > lastN {
		pairs = pairs[len(pairs)-lastN:]
	}
	pairs = ct.Formatter.TruncateToLimit(pairs, ct.MaxTokens)

	totalText := ""
	for _, pair := range pairs {
		totalText += pair[0] + pair[1]
	}
	tokenEstimate := ct.Formatter.EstimateTokens(totalText)

	return &TransferContext{
		Conversations:   pairs,
		SourceSessionID: info.SessionID,
		TokenEstimate:   tokenEstimate,
		Metadata:        map[string]any{"session_path": resolved, "provider": "claude"},
		Stats:           stats,
		SourceProvider:  "claude",
	}, nil
}

// extractFromGeneric handles codex, gemini, opencode providers.
// In the Go port, the per-provider log readers (CodexLogReader, GeminiLogReader, etc.)
// are not available. We use the session data to find the log path and parse it
// with the claude parser as a best-effort extraction, since the JSONL format
// is similar. For providers that don't have JSONL logs, we return session-not-found.
func (ct *ContextTransfer) extractFromGeneric(
	provider string,
	sessionPath string,
	lastN int,
	sourceSessionID string,
) (*TransferContext, error) {
	_, data := ct.loadSessionData(provider)

	prefixedPathKey := provider + "_session_path"
	prefixedIDKey := provider + "_session_id"

	// Resolve session path
	logPath := sessionPath
	if logPath == "" && data != nil {
		if v, ok := data[prefixedPathKey].(string); ok && v != "" {
			logPath = v
		} else if v, ok := data["session_path"].(string); ok && v != "" {
			logPath = v
		}
	}

	// Resolve session ID
	sessionID := sourceSessionID
	if sessionID == "" && data != nil {
		if v, ok := data[prefixedIDKey].(string); ok && v != "" {
			sessionID = v
		} else if v, ok := data["session_id"].(string); ok && v != "" {
			sessionID = v
		}
	}

	// Check if we have a log path to parse
	if logPath == "" {
		return nil, &SessionNotFoundError{Msg: fmt.Sprintf("No %s session log found", provider)}
	}
	if _, err := os.Stat(logPath); err != nil {
		return nil, &SessionNotFoundError{Msg: fmt.Sprintf("No %s session log found", provider)}
	}

	// Attempt to parse the JSONL log file (best-effort)
	entries, err := ct.Parser.ParseSession(logPath)
	if err != nil {
		return nil, &SessionNotFoundError{Msg: fmt.Sprintf("No %s session found: %v", provider, err)}
	}

	entries = ct.cleanEntries(entries)
	entries = ct.Deduper.DedupeMessages(entries)
	entries = ct.Deduper.CollapseToolCalls(entries)

	pairs := ct.buildPairs(entries)

	if sessionID == "" {
		sessionID = strings.TrimSuffix(filepath.Base(logPath), filepath.Ext(logPath))
	}
	if sessionID == "" {
		sessionID = "unknown"
	}

	fetchN := lastN
	if fetchN <= 0 {
		fetchN = ct.defaultFetchN()
	}

	return ct.contextFromPairs(pairs, provider, sessionID, logPath, lastN, nil), nil
}

// FormatOutput formats context for output.
func (ct *ContextTransfer) FormatOutput(context *TransferContext, format string, detailed bool) string {
	return ct.Formatter.Format(context, format, detailed)
}

// SendToProvider sends context to a provider via ask command.
func (ct *ContextTransfer) SendToProvider(context *TransferContext, provider string, format string) (bool, string) {
	supported := false
	for _, p := range SupportedProviders {
		if p == provider {
			supported = true
			break
		}
	}
	if !supported {
		return false, fmt.Sprintf("Unsupported provider: %s", provider)
	}

	formatted := ct.FormatOutput(context, format, false)

	cmd := providerCmdMap[provider]
	if cmd == "" {
		cmd = "cxb-ask"
	}

	// Pass formatted content via stdin to avoid exceeding ARG_MAX.
	execCmd := exec.Command(cmd, "--sync", "--stdin")
	execCmd.Stdin = strings.NewReader(formatted)
	result, err := execCmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if stderr == "" {
				stderr = fmt.Sprintf("Command failed with code %d", exitErr.ExitCode())
			}
			return false, stderr
		}
		if os.IsNotExist(err) {
			return false, fmt.Sprintf("Command not found: %s", cmd)
		}
		return false, err.Error()
	}
	return true, string(result)
}

// SaveTransfer saves transfer to .curdx/history/ with timestamp.
func (ct *ContextTransfer) SaveTransfer(
	context *TransferContext,
	format string,
	targetProvider string,
	filename string,
) (string, error) {
	historyDir := ct.historyDir()
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create history dir: %w", err)
	}

	extMap := map[string]string{"markdown": "md", "plain": "txt", "json": "json"}
	ext := extMap[format]
	if ext == "" {
		ext = "md"
	}

	var filePath string
	if filename != "" {
		safe := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(filename), "/", "-"), "\\", "-")
		if filepath.Ext(safe) == "" {
			safe = fmt.Sprintf("%s.%s", safe, ext)
		}
		filePath = filepath.Join(historyDir, safe)
	} else {
		ts := time.Now().Format("20060102-150405")
		sessionShort := context.SourceSessionID
		if len(sessionShort) > 8 {
			sessionShort = sessionShort[:8]
		}
		sourceProvider := strings.TrimSpace(strings.ToLower(context.SourceProvider))
		if sourceProvider == "" {
			if md := context.Metadata; md != nil {
				if p, ok := md["provider"].(string); ok {
					sourceProvider = strings.TrimSpace(strings.ToLower(p))
				}
			}
		}
		if sourceProvider == "" {
			sourceProvider = "session"
		}
		sourceProvider = strings.ReplaceAll(strings.ReplaceAll(sourceProvider, "/", "-"), "\\", "-")
		providerSuffix := ""
		if targetProvider != "" {
			providerSuffix = fmt.Sprintf("-to-%s", targetProvider)
		}
		filePath = filepath.Join(historyDir, fmt.Sprintf("%s-%s-%s%s.%s", sourceProvider, ts, sessionShort, providerSuffix, ext))
	}

	formatted := ct.FormatOutput(context, format, false)
	if err := os.WriteFile(filePath, []byte(formatted), 0o644); err != nil {
		return "", fmt.Errorf("failed to write transfer: %w", err)
	}

	return filePath, nil
}

func (ct *ContextTransfer) historyDir() string {
	workDir := ct.WorkDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}

	primary := sessionutil.ProjectConfigDir(workDir)
	legacy := sessionutil.LegacyProjectConfigDir(workDir)

	// Check if primary doesn't exist but legacy does
	_, primaryErr := os.Stat(primary)
	legacyInfo, legacyErr := os.Stat(legacy)

	if primaryErr != nil && legacyErr == nil && legacyInfo.IsDir() {
		// Try to rename legacy to primary
		if err := os.Rename(legacy, primary); err != nil {
			// Fall back to legacy
			return filepath.Join(legacy, "history")
		}
		return filepath.Join(primary, "history")
	}

	base := sessionutil.ResolveProjectConfigDir(workDir)
	os.MkdirAll(base, 0o755)
	return filepath.Join(base, "history")
}

// ExtractSessionStats extracts session statistics from a session file.
func (p *ClaudeSessionParser) ExtractSessionStats(sessionPath string) *SessionStats {
	entries, err := p.ParseSession(sessionPath)
	if err != nil {
		return nil
	}

	stats := &SessionStats{
		ToolCalls: make(map[string]int),
	}

	for _, entry := range entries {
		if entry.Role != "assistant" {
			continue
		}
		for _, tc := range entry.ToolCalls {
			name, _ := tc["name"].(string)
			if name == "" {
				name = "unknown"
			}
			stats.ToolCalls[name]++
			inp, _ := tc["input"].(map[string]any)
			if inp == nil {
				inp = map[string]any{}
			}
			execution := ToolExecution{
				Name:  name,
				Input: inp,
			}
			stats.ToolExecutions = append(stats.ToolExecutions, execution)

			// Track specific tool usages
			switch name {
			case "Write":
				if fp, ok := inp["file_path"].(string); ok && fp != "" {
					stats.FilesWritten = append(stats.FilesWritten, fp)
				}
			case "Read":
				if fp, ok := inp["file_path"].(string); ok && fp != "" {
					stats.FilesRead = append(stats.FilesRead, fp)
				}
			case "Edit":
				if fp, ok := inp["file_path"].(string); ok && fp != "" {
					stats.FilesEdited = append(stats.FilesEdited, fp)
				}
			case "Bash":
				if cmd, ok := inp["command"].(string); ok && cmd != "" {
					stats.BashCommands = append(stats.BashCommands, cmd)
				}
			case "TodoWrite":
				stats.TasksCreated++
			}
		}
	}

	return stats
}
