// Package completionhook provides async notification when CURDX delegation tasks complete.
// Source: claude_code_bridge/lib/completion_hook.py
package completionhook

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/curdx/curdx-bridge/internal/envutil"
)

// Completion status constants.
const (
	StatusCompleted  = "completed"
	StatusCancelled  = "cancelled"
	StatusFailed     = "failed"
	StatusIncomplete = "incomplete"
)

// StatusLabels maps status to a human-readable label.
var StatusLabels = map[string]string{
	StatusCompleted:  "Completed",
	StatusCancelled:  "Cancelled",
	StatusFailed:     "Failed",
	StatusIncomplete: "Incomplete",
}

// StatusMarkers maps status to a bracketed marker string.
var StatusMarkers = map[string]string{
	StatusCompleted:  "[CURDX_TASK_COMPLETED]",
	StatusCancelled:  "[CURDX_TASK_CANCELLED]",
	StatusFailed:     "[CURDX_TASK_FAILED]",
	StatusIncomplete: "[CURDX_TASK_INCOMPLETE]",
}

var validStatuses = map[string]bool{
	StatusCompleted:  true,
	StatusCancelled:  true,
	StatusFailed:     true,
	StatusIncomplete: true,
}

// NormalizeCompletionStatus normalizes a raw status string.
func NormalizeCompletionStatus(status string, doneSeen bool) string {
	raw := strings.TrimSpace(strings.ToLower(status))
	if validStatuses[raw] {
		return raw
	}
	if doneSeen {
		return StatusCompleted
	}
	return StatusIncomplete
}

// CompletionStatusLabel returns the human-readable label for a status.
func CompletionStatusLabel(status string, doneSeen bool) string {
	normalized := NormalizeCompletionStatus(status, doneSeen)
	return StatusLabels[normalized]
}

// CompletionStatusMarker returns the bracketed marker for a status.
func CompletionStatusMarker(status string, doneSeen bool) string {
	normalized := NormalizeCompletionStatus(status, doneSeen)
	return StatusMarkers[normalized]
}

// DefaultReplyForStatus returns a default reply message for non-completed statuses.
func DefaultReplyForStatus(status string, doneSeen bool) string {
	normalized := NormalizeCompletionStatus(status, doneSeen)
	switch normalized {
	case StatusCancelled:
		return "Task cancelled or timed out before completion."
	case StatusFailed:
		return "Task failed before producing a complete reply."
	case StatusIncomplete:
		return "Task ended without a confirmed completion marker."
	default:
		return ""
	}
}

// NotifyParams holds the arguments for NotifyCompletion.
type NotifyParams struct {
	Provider       string
	OutputFile     string
	Reply          string
	ReqID          string
	DoneSeen       bool
	Caller         string
	EmailReqID     string
	EmailMsgID     string
	EmailFrom      string
	WorkDir        string
	Status         string
	CallerPaneID   string
	CallerTerminal string
}

// NotifyCompletion sends an async notification that a task has completed.
// The completion hook binary is spawned as a background process.
func NotifyCompletion(p NotifyParams) {
	normalizedStatus := NormalizeCompletionStatus(p.Status, p.DoneSeen)
	runHookAsync(p, normalizedStatus)
}

func findHookScript() string {
	// Determine script search paths.
	selfDir, _ := os.Executable()
	if selfDir != "" {
		selfDir = filepath.Dir(selfDir)
	}

	candidates := []string{}
	if selfDir != "" {
		candidates = append(candidates, filepath.Join(selfDir, "curdx-completion-hook"))
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", "curdx-completion-hook"))
	}
	candidates = append(candidates, "/usr/local/bin/curdx-completion-hook")

	for _, p := range candidates {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		ext := filepath.Ext(p)
		if ext == ".cmd" || ext == ".bat" {
			continue
		}
		return p
	}
	return ""
}

func runHookAsync(p NotifyParams, status string) {
	if !envutil.EnvBool("CURDX_COMPLETION_HOOK_ENABLED", true) {
		return
	}

	script := findHookScript()
	if script == "" {
		return
	}

	caller := p.Caller
	if caller == "" {
		caller = "claude"
	}

	args := []string{
		script,
		"--provider", p.Provider,
		"--caller", caller,
		"--req-id", p.ReqID,
	}
	if p.OutputFile != "" {
		args = append(args, "--output", p.OutputFile)
	}

	// Build environment.
	env := os.Environ()
	env = append(env, "CURDX_CALLER="+caller)
	doneSeen := "0"
	if p.DoneSeen {
		doneSeen = "1"
	}
	env = append(env, "CURDX_DONE_SEEN="+doneSeen)
	env = append(env, "CURDX_COMPLETION_STATUS="+status)
	if p.EmailReqID != "" {
		env = append(env, "CURDX_EMAIL_REQ_ID="+p.EmailReqID)
	}
	if p.EmailMsgID != "" {
		env = append(env, "CURDX_EMAIL_MSG_ID="+p.EmailMsgID)
	}
	if p.EmailFrom != "" {
		env = append(env, "CURDX_EMAIL_FROM="+p.EmailFrom)
	}
	if p.WorkDir != "" {
		env = append(env, "CURDX_WORK_DIR="+p.WorkDir)
	}
	if p.CallerPaneID != "" {
		env = append(env, "CURDX_CALLER_PANE_ID="+p.CallerPaneID)
	}
	if p.CallerTerminal != "" {
		env = append(env, "CURDX_CALLER_TERMINAL="+p.CallerTerminal)
	}

	// Spawn as background process using Start() (not Run()).
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(p.Reply)
	// Detach stdout/stderr so the process runs independently.
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "[completion-hook] Error: %v\n", err)
		return
	}

	// Wait briefly to ensure the hook process has started and received input,
	// then release. This prevents the parent from exiting before the hook
	// has a chance to read stdin.
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
		// Hook completed quickly.
	case <-time.After(2 * time.Second):
		// Hook is running; proceed without waiting further.
	}
}
