// Copilot communication module.
//
// Reads replies from tmux pane-log files (raw terminal text) and sends prompts
// by injecting text into the Copilot pane via the configured backend.
//
// Source: claude_code_bridge/lib/copilot_comm.py
package comm

import (
	"os"
	"sync"
	"time"
)

// CopilotLogReader reads Copilot replies from tmux pane-log files.
type CopilotLogReader struct {
	WorkDir      string
	paneLogPath  string
	pollInterval time.Duration
	mu           sync.Mutex
}

// NewCopilotLogReader creates a new CopilotLogReader.
func NewCopilotLogReader(workDir, paneLogPath string) *CopilotLogReader {
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	poll := envFloat("COPILOT_POLL_INTERVAL", 0.05)
	poll = clampPollInterval(poll, 0.02, 0.5)
	return &CopilotLogReader{
		WorkDir:      workDir,
		paneLogPath:  paneLogPath,
		pollInterval: time.Duration(poll * float64(time.Second)),
	}
}

// SetPaneLogPath overrides the pane log path.
func (r *CopilotLogReader) SetPaneLogPath(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if path != "" {
		r.paneLogPath = path
	}
}

func (r *CopilotLogReader) resolveLogPath() string {
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

// CaptureState captures the current log offset for incremental reading.
func (r *CopilotLogReader) CaptureState() PaneLogState {
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
func (r *CopilotLogReader) WaitForMessage(state PaneLogState, timeout time.Duration) (string, PaneLogState) {
	return paneLogWaitForMessage(r, state, timeout, true)
}

// TryGetMessage performs a non-blocking read for a new assistant reply.
func (r *CopilotLogReader) TryGetMessage(state PaneLogState) (string, PaneLogState) {
	return paneLogWaitForMessage(r, state, 0, false)
}

// WaitForEvents blocks until new conversation events appear or timeout expires.
func (r *CopilotLogReader) WaitForEvents(state PaneLogState, timeout time.Duration) ([]Event, PaneLogState) {
	return paneLogWaitForEvents(r, state, timeout, true)
}

// TryGetEvents performs a non-blocking read for new conversation events.
func (r *CopilotLogReader) TryGetEvents(state PaneLogState) ([]Event, PaneLogState) {
	return paneLogWaitForEvents(r, state, 0, false)
}

// LatestMessage scans the full pane log and returns the last assistant content block.
func (r *CopilotLogReader) LatestMessage() string {
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
func (r *CopilotLogReader) LatestConversations(n int) []ConvPair {
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

// paneLogReader is the interface shared by Copilot/Codebuddy/Qwen log readers.
type paneLogReader interface {
	resolveLogPath() string
	getPollInterval() time.Duration
}

func (r *CopilotLogReader) getPollInterval() time.Duration { return r.pollInterval }

// ---------------------------------------------------------------------------
// shared pane-log polling loops
// ---------------------------------------------------------------------------

func paneLogWaitForMessage(r paneLogReader, state PaneLogState, timeout time.Duration, block bool) (string, PaneLogState) {
	deadline := time.Now().Add(timeout)
	if !block {
		deadline = time.Now()
	}
	currentState := state

	for {
		logPath := r.resolveLogPath()
		if logPath == "" {
			if !block || !time.Now().Before(deadline) {
				return "", currentState
			}
			pollSleep(r.getPollInterval())
			continue
		}
		if currentState.PaneLogPath != logPath {
			currentState.PaneLogPath = logPath
			currentState.Offset = 0
		}

		msg, newOffset, found := readNewPaneContent(logPath, currentState.Offset)
		currentState.Offset = newOffset
		if found {
			currentState.PaneLogPath = logPath
			return msg, currentState
		}

		if !block || !time.Now().Before(deadline) {
			return "", currentState
		}
		pollSleep(r.getPollInterval())
	}
}

func paneLogWaitForEvents(r paneLogReader, state PaneLogState, timeout time.Duration, block bool) ([]Event, PaneLogState) {
	deadline := time.Now().Add(timeout)
	if !block {
		deadline = time.Now()
	}
	currentState := state

	for {
		logPath := r.resolveLogPath()
		if logPath == "" {
			if !block || !time.Now().Before(deadline) {
				return nil, currentState
			}
			pollSleep(r.getPollInterval())
			continue
		}
		if currentState.PaneLogPath != logPath {
			currentState.PaneLogPath = logPath
			currentState.Offset = 0
		}

		events, newOffset := readNewPaneEvents(logPath, currentState.Offset)
		currentState.Offset = newOffset
		if len(events) > 0 {
			currentState.PaneLogPath = logPath
			return events, currentState
		}

		if !block || !time.Now().Before(deadline) {
			return nil, currentState
		}
		pollSleep(r.getPollInterval())
	}
}
