// Package memory provides session context parsing, deduplication, and formatting.
// Source: claude_code_bridge/lib/memory/
package memory

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ── Types (types.py) ──

// ConversationEntry is a single message in a conversation.
type ConversationEntry struct {
	Role       string           `json:"role"`       // "user" or "assistant"
	Content    string           `json:"content"`
	UUID       string           `json:"uuid,omitempty"`
	ParentUUID string           `json:"parent_uuid,omitempty"`
	Timestamp  string           `json:"timestamp,omitempty"`
	ToolCalls  []map[string]any `json:"tool_calls,omitempty"`
}

// ToolExecution is a complete tool execution with input and result.
type ToolExecution struct {
	ToolID  string         `json:"tool_id"`
	Name    string         `json:"name"`
	Input   map[string]any `json:"input"`
	Result  *string        `json:"result,omitempty"`
	IsError bool           `json:"is_error"`
}

// SessionStats holds statistics about a session's activity.
type SessionStats struct {
	ToolCalls      map[string]int  `json:"tool_calls"`
	ToolExecutions []ToolExecution `json:"tool_executions"`
	FilesWritten   []string        `json:"files_written"`
	FilesRead      []string        `json:"files_read"`
	FilesEdited    []string        `json:"files_edited"`
	BashCommands   []string        `json:"bash_commands"`
	TasksCreated   int             `json:"tasks_created"`
	TasksCompleted int             `json:"tasks_completed"`
}

// TransferContext holds context prepared for transfer to another provider.
type TransferContext struct {
	Conversations  [][2]string    `json:"conversations"` // [user_msg, assistant_msg] pairs
	SourceSessionID string        `json:"source_session_id"`
	TokenEstimate  int            `json:"token_estimate"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Stats          *SessionStats  `json:"stats,omitempty"`
	SourceProvider string         `json:"source_provider"`
}

// SessionInfo holds information about a session.
type SessionInfo struct {
	SessionID    string   `json:"session_id"`
	SessionPath  string   `json:"session_path"`
	ProjectPath  string   `json:"project_path,omitempty"`
	IsSidechain  bool     `json:"is_sidechain"`
	LastModified *float64 `json:"last_modified,omitempty"`
	Provider     string   `json:"provider,omitempty"`
}

// SessionNotFoundError is raised when no session can be found.
type SessionNotFoundError struct {
	Msg string
}

func (e *SessionNotFoundError) Error() string { return e.Msg }

// SessionParseError is raised when session parsing fails.
type SessionParseError struct {
	Msg string
}

func (e *SessionParseError) Error() string { return e.Msg }

// ── Deduper (deduper.py) ──

var protocolPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^\s*CURDX_REQ_ID:\s*\d{8}-\d{6}-\d{3}-\d+-\d+\s*$`),
	regexp.MustCompile(`^\s*CURDX_BEGIN:\s*\d{8}-\d{6}-\d{3}-\d+-\d+\s*$`),
	regexp.MustCompile(`^\s*CURDX_DONE:\s*\d{8}-\d{6}-\d{3}-\d+-\d+\s*$`),
	regexp.MustCompile(`^\s*\[CURDX_ASYNC_SUBMITTED[^\]]*\].*$`),
	regexp.MustCompile(`^\s*CURDX_CALLER=\w+\s*$`),
	regexp.MustCompile(`^\s*\[Request interrupted by user for tool use\]\s*$`),
	regexp.MustCompile(`^\s*The user doesn't want to proceed with this tool use\..*$`),
	regexp.MustCompile(`^\s*User rejected tool use\s*$`),
}

var noisePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`),
	regexp.MustCompile(`(?s)<env>.*?</env>`),
	regexp.MustCompile(`(?s)<rules>.*?</rules>`),
	regexp.MustCompile(`(?s)<!-- CURDX_CONFIG_START -->.*?<!-- CURDX_CONFIG_END -->`),
	regexp.MustCompile(`(?s)<local-command-caveat>.*?</local-command-caveat>`),
	regexp.MustCompile(`(?s)\[CURDX_ASYNC_SUBMITTED[^\]]*\][\s\S]*?(?:\n\n|\z)`),
}

var multiNewlineRe = regexp.MustCompile(`\n{3,}`)
var whitespaceRe = regexp.MustCompile(`\s+`)

// ConversationDeduper cleans and deduplicates conversation content.
type ConversationDeduper struct{}

// StripProtocolMarkers removes CURDX protocol markers from text.
func (d *ConversationDeduper) StripProtocolMarkers(text string) string {
	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		skip := false
		for _, re := range protocolPatterns {
			if re.MatchString(line) {
				skip = true
				break
			}
		}
		if !skip {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

// StripSystemNoise removes system noise tags from text.
func (d *ConversationDeduper) StripSystemNoise(text string) string {
	result := text
	for _, re := range noisePatterns {
		result = re.ReplaceAllString(result, "")
	}
	result = multiNewlineRe.ReplaceAllString(result, "\n\n")
	return strings.TrimSpace(result)
}

// CleanContent applies all cleaning operations.
func (d *ConversationDeduper) CleanContent(text string) string {
	text = d.StripProtocolMarkers(text)
	text = d.StripSystemNoise(text)
	return strings.TrimSpace(text)
}

// DedupeMessages removes duplicate consecutive messages.
func (d *ConversationDeduper) DedupeMessages(entries []ConversationEntry) []ConversationEntry {
	if len(entries) == 0 {
		return nil
	}
	var result []ConversationEntry
	prevHash := ""
	for _, entry := range entries {
		normalized := d.normalizeForHash(entry.Content)
		contentHash := fmt.Sprintf("%s:%x", entry.Role, sha256.Sum256([]byte(normalized)))
		if contentHash != prevHash {
			result = append(result, entry)
			prevHash = contentHash
		}
	}
	return result
}

func (d *ConversationDeduper) normalizeForHash(text string) string {
	text = whitespaceRe.ReplaceAllString(text, " ")
	return strings.ToLower(strings.TrimSpace(text))
}

// CollapseToolCalls collapses consecutive tool calls into summaries.
func (d *ConversationDeduper) CollapseToolCalls(entries []ConversationEntry) []ConversationEntry {
	if len(entries) == 0 {
		return nil
	}
	var result []ConversationEntry
	for _, entry := range entries {
		if entry.Role == "assistant" && len(entry.ToolCalls) > 0 {
			summary := summarizeTools(entry.ToolCalls)
			newContent := entry.Content
			if newContent != "" {
				newContent = fmt.Sprintf("%s\n\n[Tools: %s]", newContent, summary)
			} else {
				newContent = fmt.Sprintf("[Tools: %s]", summary)
			}
			result = append(result, ConversationEntry{
				Role:       entry.Role,
				Content:    newContent,
				UUID:       entry.UUID,
				ParentUUID: entry.ParentUUID,
				Timestamp:  entry.Timestamp,
			})
		} else {
			result = append(result, entry)
		}
	}
	return result
}

func summarizeTools(toolCalls []map[string]any) string {
	if len(toolCalls) == 0 {
		return ""
	}
	byName := map[string][]map[string]any{}
	for _, tc := range toolCalls {
		name, _ := tc["name"].(string)
		if name == "" {
			name = "unknown"
		}
		byName[name] = append(byName[name], tc)
	}
	var parts []string
	for name, calls := range byName {
		switch name {
		case "Read", "Glob", "Grep", "Edit", "Write":
			var files []string
			for _, c := range calls {
				inp, _ := c["input"].(map[string]any)
				if inp != nil {
					for _, key := range []string{"file_path", "path", "pattern"} {
						if v, ok := inp[key].(string); ok && v != "" {
							pathParts := strings.Split(v, "/")
							files = append(files, pathParts[len(pathParts)-1])
							break
						}
					}
				}
			}
			if len(files) > 3 {
				files = files[:3]
			}
			if len(files) > 0 {
				parts = append(parts, fmt.Sprintf("%s %d file(s): %s", name, len(calls), strings.Join(files, ", ")))
			} else {
				parts = append(parts, fmt.Sprintf("%s %d file(s)", name, len(calls)))
			}
		case "Bash":
			parts = append(parts, fmt.Sprintf("Bash %d command(s)", len(calls)))
		default:
			parts = append(parts, fmt.Sprintf("%s x%d", name, len(calls)))
		}
	}
	return strings.Join(parts, "; ")
}

// ── Formatter (formatter.py) ──

const charsPerToken = 4

// ContextFormatter formats conversation context for output.
type ContextFormatter struct {
	MaxTokens int
}

// NewContextFormatter creates a new ContextFormatter.
func NewContextFormatter(maxTokens int) *ContextFormatter {
	if maxTokens <= 0 {
		maxTokens = 8000
	}
	return &ContextFormatter{MaxTokens: maxTokens}
}

func providerLabel(provider string) string {
	key := strings.ToLower(strings.TrimSpace(provider))
	if key == "" {
		key = "claude"
	}
	labels := map[string]string{
		"claude":   "Claude",
		"codex":    "Codex",
		"opencode": "OpenCode",
		"auto":     "Auto",
	}
	if label, ok := labels[key]; ok {
		return label
	}
	p := strings.TrimSpace(provider)
	if p == "" {
		return p
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

// EstimateTokens estimates token count for text.
func (f *ContextFormatter) EstimateTokens(text string) int {
	return len(text) / charsPerToken
}

// TruncateToLimit truncates conversations to fit within token limit (newest first).
func (f *ContextFormatter) TruncateToLimit(conversations [][2]string, maxTokens int) [][2]string {
	if maxTokens <= 0 {
		maxTokens = f.MaxTokens
	}
	var result [][2]string
	totalTokens := 0
	// Process from newest to oldest
	for i := len(conversations) - 1; i >= 0; i-- {
		pair := conversations[i]
		pairTokens := f.EstimateTokens(pair[0] + pair[1])
		if totalTokens+pairTokens > maxTokens {
			break
		}
		result = append(result, pair)
		totalTokens += pairTokens
	}
	// Reverse
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// FormatMarkdown formats context as markdown.
func (f *ContextFormatter) FormatMarkdown(ctx *TransferContext, detailed bool) string {
	prov := ctx.SourceProvider
	if prov == "" {
		if md := ctx.Metadata; md != nil {
			if p, ok := md["provider"].(string); ok {
				prov = p
			}
		}
	}
	label := providerLabel(prov)
	now := time.Now().Format("2006-01-02 15:04:05")

	lines := []string{
		fmt.Sprintf("## Context Transfer from %s Session", label),
		"",
		fmt.Sprintf("**IMPORTANT**: This is a context handoff from a %s session.", label),
		"The previous AI assistant completed the work described below.",
		"Please review and continue from where it left off.",
		"",
		fmt.Sprintf("**Source Provider**: %s", label),
		fmt.Sprintf("**Source Session**: %s", ctx.SourceSessionID),
		fmt.Sprintf("**Transferred**: %s", now),
		fmt.Sprintf("**Conversations**: %d", len(ctx.Conversations)),
		"",
		"---",
		"",
	}

	lines = append(lines, "### Previous Conversation Context")
	lines = append(lines, "")

	for i, pair := range ctx.Conversations {
		lines = append(lines, fmt.Sprintf("#### Turn %d", i+1))
		lines = append(lines, fmt.Sprintf("**User**: %s", pair[0]))
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("**Assistant**: %s", pair[1]))
		lines = append(lines, "")
		lines = append(lines, "---")
		lines = append(lines, "")
	}

	if detailed && ctx.Stats != nil {
		lines = append(lines, "### Session Statistics")
		lines = append(lines, "")
		if len(ctx.Stats.ToolCalls) > 0 {
			lines = append(lines, "**Tool Calls**:")
			for name, count := range ctx.Stats.ToolCalls {
				lines = append(lines, fmt.Sprintf("- %s: %d", name, count))
			}
			lines = append(lines, "")
		}
		if len(ctx.Stats.FilesEdited) > 0 {
			lines = append(lines, fmt.Sprintf("**Files Edited**: %s", strings.Join(ctx.Stats.FilesEdited, ", ")))
		}
		if len(ctx.Stats.FilesWritten) > 0 {
			lines = append(lines, fmt.Sprintf("**Files Written**: %s", strings.Join(ctx.Stats.FilesWritten, ", ")))
		}
		if ctx.Stats.TasksCreated > 0 {
			lines = append(lines, fmt.Sprintf("**Tasks Created**: %d", ctx.Stats.TasksCreated))
			lines = append(lines, fmt.Sprintf("**Tasks Completed**: %d", ctx.Stats.TasksCompleted))
		}
		lines = append(lines, "")
		lines = append(lines, "---")
		lines = append(lines, "")
	}

	lines = append(lines, "**Action Required**: Review the above context and continue the work.")
	return strings.Join(lines, "\n")
}

// FormatPlain formats context as plain text.
func (f *ContextFormatter) FormatPlain(ctx *TransferContext) string {
	prov := ctx.SourceProvider
	if prov == "" {
		prov = "claude"
	}
	label := providerLabel(prov)
	now := time.Now().Format("2006-01-02 15:04:05")

	lines := []string{
		fmt.Sprintf("=== Context Transfer from %s ===", label),
		fmt.Sprintf("Provider: %s", label),
		fmt.Sprintf("Session: %s", ctx.SourceSessionID),
		fmt.Sprintf("Transferred: %s", now),
		fmt.Sprintf("Conversations: %d", len(ctx.Conversations)),
		"",
		"=== Previous Conversation ===",
		"",
	}

	for i, pair := range ctx.Conversations {
		lines = append(lines, fmt.Sprintf("--- Turn %d ---", i+1))
		lines = append(lines, fmt.Sprintf("User: %s", pair[0]))
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("Assistant: %s", pair[1]))
		lines = append(lines, "")
	}

	lines = append(lines, "=== End of Context ===")
	return strings.Join(lines, "\n")
}

// FormatJSON formats context as JSON.
func (f *ContextFormatter) FormatJSON(ctx *TransferContext) string {
	prov := strings.ToLower(strings.TrimSpace(ctx.SourceProvider))
	if prov == "" {
		if md := ctx.Metadata; md != nil {
			if p, ok := md["provider"].(string); ok {
				prov = strings.ToLower(strings.TrimSpace(p))
			}
		}
	}
	if prov == "" {
		prov = "claude"
	}

	convs := make([]map[string]string, len(ctx.Conversations))
	for i, pair := range ctx.Conversations {
		convs[i] = map[string]string{"user": pair[0], "assistant": pair[1]}
	}

	data := map[string]any{
		"source_provider":   prov,
		"source_session_id": ctx.SourceSessionID,
		"transferred_at":    time.Now().Format(time.RFC3339),
		"token_estimate":    ctx.TokenEstimate,
		"conversations":     convs,
		"metadata":          ctx.Metadata,
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	return string(b)
}

// Format formats context in the specified format.
func (f *ContextFormatter) Format(ctx *TransferContext, format string, detailed bool) string {
	switch format {
	case "plain":
		return f.FormatPlain(ctx)
	case "json":
		return f.FormatJSON(ctx)
	default:
		return f.FormatMarkdown(ctx, detailed)
	}
}

// ── Session Parser (session_parser.py) ──

// ClaudeSessionParser parses Claude JSONL session files.
type ClaudeSessionParser struct {
	Root string
}

// NewClaudeSessionParser creates a new parser with the given root or default.
func NewClaudeSessionParser(root string) *ClaudeSessionParser {
	if root == "" {
		root = os.Getenv("CLAUDE_PROJECTS_ROOT")
		if root == "" {
			root = os.Getenv("CLAUDE_PROJECT_ROOT")
		}
		if root == "" {
			home, _ := os.UserHomeDir()
			root = filepath.Join(home, ".claude", "projects")
		}
	}
	return &ClaudeSessionParser{Root: root}
}

// ResolveSession resolves the session file path.
func (p *ClaudeSessionParser) ResolveSession(workDir string, sessionPath string) (string, error) {
	if sessionPath != "" {
		if _, err := os.Stat(sessionPath); err == nil {
			return sessionPath, nil
		}
	}

	// Try sessions-index.json
	if s := p.resolveFromIndex(workDir); s != "" {
		return s, nil
	}

	// Scan project directory
	if s := p.scanProjectDir(workDir); s != "" {
		return s, nil
	}

	// Fallback
	if os.Getenv("CLAUDE_ALLOW_ANY_PROJECT_SCAN") == "1" {
		if s := p.scanAllProjects(); s != "" {
			return s, nil
		}
	}

	return "", &SessionNotFoundError{Msg: fmt.Sprintf("No session found for %s", workDir)}
}

func (p *ClaudeSessionParser) resolveFromIndex(workDir string) string {
	indexPath := filepath.Join(p.Root, "sessions-index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return ""
	}

	var index struct {
		Sessions []struct {
			SessionID    string  `json:"sessionId"`
			ProjectPath  string  `json:"projectPath"`
			IsSidechain  bool    `json:"isSidechain"`
			LastModified float64 `json:"lastModified"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return ""
	}

	absWorkDir, _ := filepath.Abs(workDir)

	type candidate struct {
		sessionID    string
		lastModified float64
	}
	var candidates []candidate

	for _, s := range index.Sessions {
		if s.IsSidechain {
			continue
		}
		if s.ProjectPath != "" && (absWorkDir == s.ProjectPath || strings.HasPrefix(absWorkDir, s.ProjectPath+string(filepath.Separator))) {
			candidates = append(candidates, candidate{s.SessionID, s.LastModified})
		}
	}

	if len(candidates) == 0 {
		for _, s := range index.Sessions {
			if !s.IsSidechain {
				candidates = append(candidates, candidate{s.SessionID, s.LastModified})
			}
		}
	}

	if len(candidates) == 0 {
		return ""
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].lastModified > candidates[j].lastModified
	})

	return p.findSessionFile(candidates[0].sessionID, workDir)
}

func (p *ClaudeSessionParser) findSessionFile(sessionID, workDir string) string {
	projDir := p.getProjectDir(workDir)
	if projDir != "" {
		candidate := filepath.Join(projDir, sessionID+".jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	entries, err := os.ReadDir(p.Root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(p.Root, e.Name(), sessionID+".jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func (p *ClaudeSessionParser) getProjectDir(workDir string) string {
	absWorkDir, _ := filepath.Abs(workDir)
	re := regexp.MustCompile(`[^A-Za-z0-9]`)
	key := re.ReplaceAllString(absWorkDir, "-")
	projDir := filepath.Join(p.Root, key)
	if _, err := os.Stat(projDir); err == nil {
		return projDir
	}
	return ""
}

func (p *ClaudeSessionParser) scanProjectDir(workDir string) string {
	projDir := p.getProjectDir(workDir)
	if projDir == "" {
		return ""
	}

	entries, err := os.ReadDir(projDir)
	if err != nil {
		return ""
	}

	type fileInfo struct {
		path  string
		mtime time.Time
	}
	var jsonlFiles []fileInfo
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		jsonlFiles = append(jsonlFiles, fileInfo{filepath.Join(projDir, e.Name()), info.ModTime()})
	}

	if len(jsonlFiles) == 0 {
		return ""
	}

	sort.Slice(jsonlFiles, func(i, j int) bool {
		return jsonlFiles[i].mtime.After(jsonlFiles[j].mtime)
	})
	return jsonlFiles[0].path
}

func (p *ClaudeSessionParser) scanAllProjects() string {
	if _, err := os.Stat(p.Root); err != nil {
		return ""
	}

	var bestPath string
	var bestMtime time.Time

	entries, err := os.ReadDir(p.Root)
	if err != nil {
		return ""
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		subEntries, err := os.ReadDir(filepath.Join(p.Root, e.Name()))
		if err != nil {
			continue
		}
		for _, se := range subEntries {
			if !strings.HasSuffix(se.Name(), ".jsonl") {
				continue
			}
			info, err := se.Info()
			if err != nil {
				continue
			}
			if info.ModTime().After(bestMtime) {
				bestMtime = info.ModTime()
				bestPath = filepath.Join(p.Root, e.Name(), se.Name())
			}
		}
	}

	return bestPath
}

// ParseSession parses a session JSONL file into conversation entries.
func (p *ClaudeSessionParser) ParseSession(sessionPath string) ([]ConversationEntry, error) {
	if _, err := os.Stat(sessionPath); err != nil {
		return nil, &SessionNotFoundError{Msg: fmt.Sprintf("Session file not found: %s", sessionPath)}
	}

	data, err := os.ReadFile(sessionPath)
	if err != nil {
		return nil, &SessionParseError{Msg: fmt.Sprintf("Failed to read session file: %v", err)}
	}

	var entries []ConversationEntry
	errors := 0
	total := 0

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		total++
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			errors++
			continue
		}
		if entry := p.parseEntry(obj); entry != nil {
			entries = append(entries, *entry)
		}
	}

	if total > 0 && float64(errors)/float64(total) > 0.5 {
		return nil, &SessionParseError{
			Msg: fmt.Sprintf("Too many parse errors: %d/%d lines failed", errors, total),
		}
	}

	return entries, nil
}

func (p *ClaudeSessionParser) parseEntry(obj map[string]any) *ConversationEntry {
	msgType, _ := obj["type"].(string)

	if msgType == "user" {
		message, _ := obj["message"].(map[string]any)
		content := extractContent(message)
		if content != "" {
			uuid, _ := obj["uuid"].(string)
			parentUUID, _ := obj["parentUuid"].(string)
			timestamp, _ := obj["timestamp"].(string)
			return &ConversationEntry{
				Role:       "user",
				Content:    content,
				UUID:       uuid,
				ParentUUID: parentUUID,
				Timestamp:  timestamp,
			}
		}
	}

	if msgType == "assistant" {
		message, _ := obj["message"].(map[string]any)
		content := extractContent(message)
		toolCalls := extractToolCalls(message)
		if content != "" || len(toolCalls) > 0 {
			uuid, _ := obj["uuid"].(string)
			parentUUID, _ := obj["parentUuid"].(string)
			timestamp, _ := obj["timestamp"].(string)
			return &ConversationEntry{
				Role:       "assistant",
				Content:    content,
				UUID:       uuid,
				ParentUUID: parentUUID,
				Timestamp:  timestamp,
				ToolCalls:  toolCalls,
			}
		}
	}

	return nil
}

func extractContent(message map[string]any) string {
	if message == nil {
		return ""
	}

	content := message["content"]
	if s, ok := content.(string); ok {
		return s
	}

	if blocks, ok := content.([]any); ok {
		var texts []string
		for _, block := range blocks {
			if m, ok := block.(map[string]any); ok {
				if t, _ := m["type"].(string); t == "text" {
					if text, _ := m["text"].(string); text != "" {
						texts = append(texts, text)
					}
				}
			} else if s, ok := block.(string); ok {
				texts = append(texts, s)
			}
		}
		return strings.Join(texts, "\n")
	}

	return ""
}

func extractToolCalls(message map[string]any) []map[string]any {
	if message == nil {
		return nil
	}

	content, ok := message["content"].([]any)
	if !ok {
		return nil
	}

	var toolCalls []map[string]any
	for _, block := range content {
		m, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t == "tool_use" {
			name, _ := m["name"].(string)
			input, _ := m["input"].(map[string]any)
			if input == nil {
				input = map[string]any{}
			}
			toolCalls = append(toolCalls, map[string]any{
				"name":  name,
				"input": input,
			})
		}
	}

	return toolCalls
}

// GetSessionInfo returns information about a session.
func (p *ClaudeSessionParser) GetSessionInfo(sessionPath string) *SessionInfo {
	info := &SessionInfo{
		SessionID:   strings.TrimSuffix(filepath.Base(sessionPath), ".jsonl"),
		SessionPath: sessionPath,
	}
	if fi, err := os.Stat(sessionPath); err == nil {
		mtime := float64(fi.ModTime().Unix())
		info.LastModified = &mtime
	}
	return info
}
