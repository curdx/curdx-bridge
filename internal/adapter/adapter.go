// Package adapter defines the provider adapter interface and types.
// Source: claude_code_bridge/lib/askd/adapters/base.py
package adapter

import (
	"sync"

	"github.com/curdx/curdx-bridge/internal/providers"
)

// ProviderRequest is the unified request structure for all providers.
type ProviderRequest struct {
	ClientID       string  `json:"client_id"`
	WorkDir        string  `json:"work_dir"`
	TimeoutS       float64 `json:"timeout_s"`
	Quiet          bool    `json:"quiet"`
	Message        string  `json:"message"`
	Caller         string  `json:"caller"`
	OutputPath     string  `json:"output_path,omitempty"`
	ReqID          string  `json:"req_id,omitempty"`
	NoWrap         bool    `json:"no_wrap,omitempty"`
	EmailReqID     string  `json:"email_req_id,omitempty"`
	EmailMsgID     string  `json:"email_msg_id,omitempty"`
	EmailFrom      string  `json:"email_from,omitempty"`
	Instance       string  `json:"instance,omitempty"`
	CallerPaneID   string  `json:"caller_pane_id,omitempty"`
	CallerTerminal string  `json:"caller_terminal,omitempty"`
}

// ProviderResult is the unified result structure for all providers.
type ProviderResult struct {
	ExitCode     int            `json:"exit_code"`
	Reply        string         `json:"reply"`
	ReqID        string         `json:"req_id"`
	SessionKey   string         `json:"session_key"`
	DoneSeen     bool           `json:"done_seen"`
	DoneMs       *int           `json:"done_ms,omitempty"`
	AnchorSeen   bool           `json:"anchor_seen,omitempty"`
	AnchorMs     *int           `json:"anchor_ms,omitempty"`
	FallbackScan bool           `json:"fallback_scan,omitempty"`
	LogPath      string         `json:"log_path,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"`
	Status       string         `json:"status,omitempty"`
}

// QueuedTask is a task queued for processing by a provider adapter.
type QueuedTask struct {
	Request     ProviderRequest
	CreatedMs   int64
	ReqID       string
	DoneEvent   chan struct{}
	Result      *ProviderResult
	Cancelled   bool
	CancelEvent chan struct{}
	mu          sync.Mutex
}

// SetResult sets the result and signals done.
func (t *QueuedTask) SetResult(r *ProviderResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Result = r
	select {
	case <-t.DoneEvent:
	default:
		close(t.DoneEvent)
	}
}

// Cancel marks the task as cancelled.
func (t *QueuedTask) Cancel() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Cancelled = true
	if t.CancelEvent != nil {
		select {
		case <-t.CancelEvent:
		default:
			close(t.CancelEvent)
		}
	}
}

// IsCancelled returns true if the task was cancelled.
func (t *QueuedTask) IsCancelled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Cancelled
}

// GetResult returns the current result (may be nil).
func (t *QueuedTask) GetResult() *ProviderResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Result
}

// SignalDone ensures the DoneEvent channel is closed.
func (t *QueuedTask) SignalDone() {
	t.mu.Lock()
	defer t.mu.Unlock()
	select {
	case <-t.DoneEvent:
	default:
		close(t.DoneEvent)
	}
}

// BaseProviderAdapter is the interface that all provider adapters must implement.
type BaseProviderAdapter interface {
	// Key returns the provider key (e.g., "codex", "gemini").
	Key() string
	// Spec returns the provider daemon specification.
	Spec() providers.ProviderDaemonSpec
	// SessionFilename returns the session file name (e.g., ".codex-session").
	SessionFilename() string
	// LoadSession loads session for the given work directory.
	LoadSession(workDir string, instance string) (any, error)
	// ComputeSessionKey computes a unique session key for routing.
	ComputeSessionKey(session any, instance string) string
	// HandleTask handles a queued task and returns the result.
	HandleTask(task *QueuedTask) *ProviderResult
	// HandleException handles an exception during task processing.
	HandleException(err error, task *QueuedTask) *ProviderResult
	// OnStart is called when the daemon starts.
	OnStart()
	// OnStop is called when the daemon stops.
	OnStop()
}

// DefaultHandleException provides the default exception handling.
func DefaultHandleException(key string, err error, task *QueuedTask) *ProviderResult {
	return &ProviderResult{
		ExitCode:   1,
		Reply:      err.Error(),
		ReqID:      task.ReqID,
		SessionKey: key + ":unknown",
		DoneSeen:   false,
		Status:     "failed",
	}
}
