// Qwen communication module.
//
// Reads replies from tmux pane-log files (raw terminal text) and sends prompts
// by injecting text into the Qwen pane via the configured backend.
//
// Source: claude_code_bridge/lib/qwen_comm.py
package comm

import (
	"os"
	"sync"
	"time"
)

// QwenLogReader reads Qwen replies from tmux pane-log files.
type QwenLogReader struct {
	WorkDir      string
	paneLogPath  string
	pollInterval time.Duration
	mu           sync.Mutex
}

// NewQwenLogReader creates a new QwenLogReader.
func NewQwenLogReader(workDir, paneLogPath string) *QwenLogReader {
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	poll := envFloat("QWEN_POLL_INTERVAL", 0.05)
	poll = clampPollInterval(poll, 0.02, 0.5)
	return &QwenLogReader{
		WorkDir:      workDir,
		paneLogPath:  paneLogPath,
		pollInterval: time.Duration(poll * float64(time.Second)),
	}
}

// SetPaneLogPath overrides the pane log path.
func (r *QwenLogReader) SetPaneLogPath(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if path != "" {
		r.paneLogPath = path
	}
}

func (r *QwenLogReader) resolveLogPath() string {
	r.mu.Lock()
	p := r.paneLogPath
	r.mu.Unlock()
	if p == "" {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func (r *QwenLogReader) getPollInterval() time.Duration { return r.pollInterval }

// CaptureState captures the current log offset for incremental reading.
func (r *QwenLogReader) CaptureState() PaneLogState {
	logPath := r.resolveLogPath()
	var offset int64
	if logPath != "" {
		if fi, err := os.Stat(logPath); err == nil {
			offset = fi.Size()
		}
	}
	return PaneLogState{PaneLogPath: logPath, Offset: offset}
}

// WaitForMessage blocks until a new assistant reply appears or timeout expires.
func (r *QwenLogReader) WaitForMessage(state PaneLogState, timeout time.Duration) (string, PaneLogState) {
	return paneLogWaitForMessage(r, state, timeout, true)
}

// TryGetMessage performs a non-blocking read for a new assistant reply.
func (r *QwenLogReader) TryGetMessage(state PaneLogState) (string, PaneLogState) {
	return paneLogWaitForMessage(r, state, 0, false)
}

// WaitForEvents blocks until new conversation events appear or timeout expires.
func (r *QwenLogReader) WaitForEvents(state PaneLogState, timeout time.Duration) ([]Event, PaneLogState) {
	return paneLogWaitForEvents(r, state, timeout, true)
}

// TryGetEvents performs a non-blocking read for new conversation events.
func (r *QwenLogReader) TryGetEvents(state PaneLogState) ([]Event, PaneLogState) {
	return paneLogWaitForEvents(r, state, 0, false)
}

// LatestMessage scans the full pane log and returns the last assistant content block.
func (r *QwenLogReader) LatestMessage() string {
	logPath := r.resolveLogPath()
	if logPath == "" {
		return ""
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	clean := StripANSI(string(data))
	blocks := extractAssistantBlocks(clean)
	if len(blocks) > 0 {
		return blocks[len(blocks)-1]
	}
	return ""
}

// LatestConversations returns up to n recent (user, assistant) pairs from the pane log.
func (r *QwenLogReader) LatestConversations(n int) []ConvPair {
	logPath := r.resolveLogPath()
	if logPath == "" {
		return nil
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}
	clean := StripANSI(string(data))
	pairs := extractConversationPairs(clean)
	if n < 1 {
		n = 1
	}
	if len(pairs) > n {
		return pairs[len(pairs)-n:]
	}
	return pairs
}
