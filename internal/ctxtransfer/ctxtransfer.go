// Package ctxtransfer provides auto context transfer utilities.
// Source: claude_code_bridge/lib/ctx_transfer_utils.py
package ctxtransfer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/curdx/curdx-bridge/internal/comm"
	"github.com/curdx/curdx-bridge/internal/envutil"
	"github.com/curdx/curdx-bridge/internal/memory"
	"github.com/curdx/curdx-bridge/internal/projectid"
	"github.com/curdx/curdx-bridge/internal/sessionutil"
)

var (
	autoTransferMu   sync.Mutex
	autoTransferSeen = map[string]float64{}
)

func normalizePathForMatch(value string) string {
	result := projectid.NormalizeWorkDir(value)
	if result != "" {
		return result
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return value
	}
	return abs
}

func isCurrentWorkDir(workDir string) bool {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return normalizePathForMatch(cwd) == normalizePathForMatch(workDir)
}

func autoTransferKey(provider, workDir, sessionPath, sessionID, projectID string) string {
	return fmt.Sprintf("%s::%s::%s::%s::%s", provider, workDir, sessionPath, sessionID, projectID)
}

// MaybeAutoTransfer triggers an automatic context transfer if conditions are met.
// It runs the actual transfer in a background goroutine.
func MaybeAutoTransfer(provider, workDir, sessionPath, sessionID, projectID string) {
	if !envutil.EnvBool("CURDX_CTX_TRANSFER_ON_SESSION_SWITCH", true) {
		return
	}
	if sessionPath == "" && sessionID == "" {
		return
	}

	if workDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return
		}
		workDir = cwd
	}

	if !isCurrentWorkDir(workDir) {
		return
	}

	key := autoTransferKey(provider, workDir, sessionPath, sessionID, projectID)
	now := float64(time.Now().Unix())

	autoTransferMu.Lock()
	if _, seen := autoTransferSeen[key]; seen {
		autoTransferMu.Unlock()
		return
	}
	// Clean old entries
	for k, ts := range autoTransferSeen {
		if now-ts > 3600 {
			delete(autoTransferSeen, k)
		}
	}
	autoTransferSeen[key] = now
	autoTransferMu.Unlock()

	// Run transfer in background goroutine
	go func() {
		runAutoTransfer(provider, workDir, sessionPath, sessionID, projectID)
	}()
}

func runAutoTransfer(provider, workDir, sessionPath, sessionID, projectID string) {
	// Read environment configuration (mirrors Python _run() inner function)
	lastN := envutil.EnvInt("CURDX_CTX_TRANSFER_LAST_N", 0)
	maxTokens := envutil.EnvInt("CURDX_CTX_TRANSFER_MAX_TOKENS", 8000)
	fmtStr := strings.ToLower(strings.TrimSpace(os.Getenv("CURDX_CTX_TRANSFER_FORMAT")))
	if fmtStr == "" {
		fmtStr = "markdown"
	}

	// Build the transfer context by extracting conversations from the source provider
	ctx := extractConversationsForProvider(provider, workDir, sessionPath, sessionID, projectID, lastN, maxTokens)
	if ctx == nil || len(ctx.Conversations) == 0 {
		return
	}

	// Format and save to history directory
	formatter := memory.NewContextFormatter(maxTokens)
	formatted := formatter.Format(ctx, fmtStr, false)
	if strings.TrimSpace(formatted) == "" {
		return
	}

	historyDir := resolveHistoryDir(workDir)
	if historyDir == "" {
		return
	}
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return
	}

	ts := time.Now().Format("20060102-150405")
	sid := sessionID
	if sid == "" && sessionPath != "" {
		sid = strings.TrimSuffix(filepath.Base(sessionPath), filepath.Ext(sessionPath))
	}
	if sid == "" {
		sid = "unknown"
	}
	ext := "md"
	switch fmtStr {
	case "plain":
		ext = "txt"
	case "json":
		ext = "json"
	}
	filename := fmt.Sprintf("%s-%s-%s.%s", provider, ts, sid, ext)
	savePath := filepath.Join(historyDir, filename)
	_ = os.WriteFile(savePath, []byte(formatted), 0o644)
}

func extractConversationsForProvider(provider, workDir, sessionPath, sessionID, projectID string, lastN, maxTokens int) *memory.TransferContext {
	fetchN := lastN
	if fetchN <= 0 {
		fetchN = 50
	}

	var pairs []comm.ConvPair

	switch provider {
	case "codex":
		reader := comm.NewCodexLogReader("", sessionPath, sessionID, workDir)
		pairs = reader.LatestConversations(fetchN)
	case "gemini":
		reader := comm.NewGeminiLogReader("", workDir)
		if sessionPath != "" {
			reader.SetPreferredSession(sessionPath)
		}
		pairs = reader.LatestConversations(fetchN)
	case "claude":
		reader := comm.NewClaudeLogReader("", workDir)
		if sessionPath != "" {
			reader.SetPreferredSession(sessionPath)
		}
		pairs = reader.LatestConversations(fetchN)
	case "opencode":
		pid := projectID
		if pid == "" {
			pid = "global"
		}
		var opts []comm.OpenCodeOption
		if sessionID != "" {
			opts = append(opts, comm.WithSessionIDFilter(sessionID))
		}
		reader := comm.NewOpenCodeLogReader("", workDir, pid, opts...)
		pairs = reader.LatestConversations(fetchN)
	default:
		return nil
	}

	if len(pairs) == 0 {
		return nil
	}

	// Clean and deduplicate
	deduper := &memory.ConversationDeduper{}
	formatter := memory.NewContextFormatter(maxTokens)
	var conversations [][2]string
	var prevHash string
	for _, p := range pairs {
		cleanedUser := deduper.CleanContent(p.User)
		cleanedAssistant := deduper.CleanContent(p.Assistant)
		if cleanedUser == "" && cleanedAssistant == "" {
			continue
		}
		pairHash := fmt.Sprintf("%d::%d", hashString(cleanedUser), hashString(cleanedAssistant))
		if pairHash == prevHash {
			continue
		}
		conversations = append(conversations, [2]string{cleanedUser, cleanedAssistant})
		prevHash = pairHash
	}
	if lastN > 0 && len(conversations) > lastN {
		conversations = conversations[len(conversations)-lastN:]
	}
	conversations = formatter.TruncateToLimit(conversations, maxTokens)
	if len(conversations) == 0 {
		return nil
	}

	totalText := ""
	for _, c := range conversations {
		totalText += c[0] + c[1]
	}
	tokenEstimate := formatter.EstimateTokens(totalText)

	sid := sessionID
	if sid == "" && sessionPath != "" {
		sid = strings.TrimSuffix(filepath.Base(sessionPath), filepath.Ext(sessionPath))
	}
	if sid == "" {
		sid = "unknown"
	}

	meta := map[string]interface{}{"provider": provider}
	if sessionPath != "" {
		meta["session_path"] = sessionPath
	}

	return &memory.TransferContext{
		Conversations:   conversations,
		SourceSessionID: sid,
		TokenEstimate:   tokenEstimate,
		Metadata:        meta,
		SourceProvider:  provider,
	}
}

func resolveHistoryDir(workDir string) string {
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return ""
		}
	}
	primary := sessionutil.ProjectConfigDir(workDir)
	legacy := sessionutil.LegacyProjectConfigDir(workDir)

	fi, err := os.Stat(primary)
	if err != nil || !fi.IsDir() {
		if fi2, err2 := os.Stat(legacy); err2 == nil && fi2.IsDir() {
			// Try to rename legacy to primary
			if os.Rename(legacy, primary) == nil {
				return filepath.Join(primary, "history")
			}
			return filepath.Join(legacy, "history")
		}
	}
	base := sessionutil.ResolveProjectConfigDir(workDir)
	return filepath.Join(base, "history")
}

func hashString(s string) uint64 {
	var h uint64 = 5381
	for i := 0; i < len(s); i++ {
		h = ((h << 5) + h) + uint64(s[i])
	}
	return h
}
