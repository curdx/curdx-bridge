// Codex provider adapter for the unified ask daemon.
// Source: claude_code_bridge/lib/askd/adapters/codex.py
package adapter

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/curdx/curdx-bridge/internal/comm"
	"github.com/curdx/curdx-bridge/internal/completionhook"
	"github.com/curdx/curdx-bridge/internal/protocol"
	"github.com/curdx/curdx-bridge/internal/providers"
	"github.com/curdx/curdx-bridge/internal/runtime"
	"github.com/curdx/curdx-bridge/internal/session"
	"github.com/curdx/curdx-bridge/internal/terminal"
)

// CodexAdapter implements BaseProviderAdapter for the Codex provider.
type CodexAdapter struct{}

func (a *CodexAdapter) Key() string                        { return "codex" }
func (a *CodexAdapter) Spec() providers.ProviderDaemonSpec { return providers.CaskdSpec }
func (a *CodexAdapter) SessionFilename() string            { return ".codex-session" }
func (a *CodexAdapter) OnStart()                           {}
func (a *CodexAdapter) OnStop()                            {}

func (a *CodexAdapter) LoadSession(workDir string, instance string) (any, error) {
	s := session.LoadCodexSession(workDir, instance)
	if s == nil {
		return nil, nil
	}
	return s, nil
}

func (a *CodexAdapter) ComputeSessionKey(sess any, instance string) string {
	s, ok := sess.(*session.CodexProjectSession)
	if !ok || s == nil {
		return "codex:unknown"
	}
	return session.ComputeCodexSessionKey(s, instance)
}

func (a *CodexAdapter) HandleException(err error, task *QueuedTask) *ProviderResult {
	return DefaultHandleException("codex", err, task)
}

func codexWriteLog(line string) {
	runtime.WriteLog(runtime.LogPath(providers.CaskdSpec.LogFileName), line)
}

func (a *CodexAdapter) HandleTask(task *QueuedTask) *ProviderResult {
	startedMs := nowMs()
	startedAt := time.Now()
	req := task.Request
	codexWriteLog(fmt.Sprintf("[INFO] start provider=codex req_id=%s work_dir=%s caller=%s", task.ReqID, req.WorkDir, req.Caller))

	instance := req.Instance
	sess := session.LoadCodexSession(req.WorkDir, instance)
	var sessionKey string
	if sess != nil {
		sessionKey = session.ComputeCodexSessionKey(sess, instance)
	} else {
		sessionKey = "codex:unknown"
	}

	if sess == nil {
		return &ProviderResult{
			ExitCode:   1,
			Reply:      "No active Codex session found for work_dir.",
			ReqID:      task.ReqID,
			SessionKey: sessionKey,
			DoneSeen:   false,
			Status:     completionhook.StatusFailed,
		}
	}

	paneResult := sess.EnsurePane()
	if !paneResult.OK {
		return &ProviderResult{
			ExitCode:   1,
			Reply:      fmt.Sprintf("Session pane not available: %s", paneResult.Err),
			ReqID:      task.ReqID,
			SessionKey: sessionKey,
			DoneSeen:   false,
			Status:     completionhook.StatusFailed,
		}
	}
	paneID := paneResult.PaneID

	backend := terminal.GetBackendForSession(sess.Data)
	if backend == nil {
		return &ProviderResult{
			ExitCode:   1,
			Reply:      "Terminal backend not available",
			ReqID:      task.ReqID,
			SessionKey: sessionKey,
			DoneSeen:   false,
			Status:     completionhook.StatusFailed,
		}
	}

	prompt := protocol.WrapCodexPrompt(req.Message, task.ReqID)
	preferredLog := sess.CodexSessionPath()
	codexSessionID := sess.CodexSessionID()
	reader := comm.NewCodexLogReader("", preferredLog, codexSessionID, sess.WorkDir())
	state := reader.CaptureState()
	if err := backend.SendText(paneID, prompt); err != nil {
		codexWriteLog(fmt.Sprintf("[ERROR] SendText failed req_id=%s: %v", task.ReqID, err))
		return &ProviderResult{
			ExitCode:   1,
			Reply:      fmt.Sprintf("failed to send text: %v", err),
			ReqID:      task.ReqID,
			SessionKey: sessionKey,
			DoneSeen:   false,
			Status:     completionhook.StatusFailed,
		}
	}

	var deadline *time.Time
	if req.TimeoutS >= 0.0 {
		d := time.Now().Add(time.Duration(req.TimeoutS * float64(time.Second)))
		deadline = &d
	}

	var chunks []string
	anchorSeen := false
	doneSeen := false
	var anchorMs *int
	var doneMs *int
	fallbackScan := false

	// Idle timeout detection for degraded completion
	idleTimeout := envFloatDefault("CURDX_CASKD_IDLE_TIMEOUT", 8.0)
	lastReplySnapshot := ""
	lastReplyChangedAt := time.Now()

	var anchorCollectGrace time.Time
	graceLimit := time.Now().Add(2 * time.Second)
	if deadline != nil && deadline.Before(graceLimit) {
		anchorCollectGrace = *deadline
	} else {
		anchorCollectGrace = graceLimit
	}

	lastPaneCheck := time.Now()
	defaultInterval := 2.0
	if terminal.IsWindows() {
		defaultInterval = 5.0
	}
	paneCheckInterval := envFloatDefault("CURDX_CASKD_PANE_CHECK_INTERVAL", defaultInterval)
	staleGraceS := envFloatDefault("CURDX_CASKD_STALE_LOG_GRACE_SECONDS", 2.5)
	staleCheckInterval := envFloatDefault("CURDX_CASKD_STALE_LOG_CHECK_INTERVAL", 1.0)
	staleThresholdS := envFloatDefault("CURDX_CODEX_STALE_LOG_SECONDS", 10.0)
	lastStaleCheck := time.Now()

	for {
		// Check for cancellation
		if task.CancelEvent != nil {
			select {
			case <-task.CancelEvent:
				codexWriteLog(fmt.Sprintf("[INFO] Task cancelled during wait loop: req_id=%s", task.ReqID))
				goto loopEnd
			default:
			}
		}

		if deadline != nil {
			remaining := time.Until(*deadline)
			if remaining <= 0 {
				break
			}
		}

		var waitStep time.Duration
		if deadline != nil {
			remaining := time.Until(*deadline)
			waitStep = min(remaining, 500*time.Millisecond)
		} else {
			waitStep = 500 * time.Millisecond
		}

		if time.Since(lastPaneCheck).Seconds() >= paneCheckInterval {
			alive := backend.IsAlive(paneID)
			if !alive {
				codexWriteLog(fmt.Sprintf("[ERROR] Pane %s died during request req_id=%s", paneID, task.ReqID))
				var codexLogPath string
				lp := reader.CurrentLogPath()
				if lp != "" {
					codexLogPath = lp
				}
				return &ProviderResult{
					ExitCode:     1,
					Reply:        "Codex pane died during request",
					ReqID:        task.ReqID,
					SessionKey:   sessionKey,
					DoneSeen:     false,
					AnchorSeen:   anchorSeen,
					FallbackScan: fallbackScan,
					AnchorMs:     anchorMs,
					LogPath:      codexLogPath,
					Status:       completionhook.StatusFailed,
				}
			}
			lastPaneCheck = time.Now()
		}

		event, newState := reader.WaitForEvent(state, waitStep)
		state = newState

		if event == nil {
			// Stale log detection: if no anchor and no chunks yet,
			// check whether a newer session log appeared (e.g. after pane restart).
			if !anchorSeen && len(chunks) == 0 {
				now := time.Now()
				if now.Sub(startedAt).Seconds() >= staleGraceS && now.Sub(lastStaleCheck).Seconds() >= staleCheckInterval {
					lastStaleCheck = now
					latestReader := comm.NewCodexLogReader("", "", "", sess.WorkDir())
					latestLog := latestReader.CurrentLogPath()
					currentLog := state.LogPath
					if latestLog != "" && latestLog != currentLog && isLogStale(currentLog, latestLog, staleThresholdS) {
						reader = comm.NewCodexLogReader("", latestLog, "", sess.WorkDir())
						state = reader.CaptureState()
						fallbackScan = true
						newSessionID := comm.ExtractSessionID(latestLog)
						sess.UpdateCodexLogBinding(latestLog, newSessionID)
						preferredLog = latestLog
						if newSessionID != "" {
							codexSessionID = newSessionID
						}
						codexWriteLog(fmt.Sprintf("[WARN] stale codex log detected; switching to %s", latestLog))
					}
				}
			}
			continue
		}

		if event.Role == "user" {
			if strings.Contains(event.Text, fmt.Sprintf("%s %s", protocol.REQIDPrefix, task.ReqID)) {
				anchorSeen = true
				if anchorMs == nil {
					v := int(nowMs() - startedMs)
					anchorMs = &v
				}
			}
			continue
		}

		if event.Role != "assistant" {
			continue
		}

		// Use grace window
		if !anchorSeen && time.Now().Before(anchorCollectGrace) {
			continue
		}

		chunks = append(chunks, event.Text)
		combined := strings.Join(chunks, "\n")
		if protocol.IsDoneText(combined, task.ReqID) {
			doneSeen = true
			v := int(nowMs() - startedMs)
			doneMs = &v
			break
		}

		// Idle-timeout: detect when Codex finished but forgot CURDX_DONE
		if combined != lastReplySnapshot {
			lastReplySnapshot = combined
			lastReplyChangedAt = time.Now()
		} else if combined != "" && time.Since(lastReplyChangedAt).Seconds() >= idleTimeout {
			codexWriteLog(fmt.Sprintf(
				"[WARN] Codex reply idle for %.0fs without CURDX_DONE, accepting as complete req_id=%s",
				idleTimeout, task.ReqID,
			))
			doneSeen = true
			v := int(nowMs() - startedMs)
			doneMs = &v
			break
		}
	}

loopEnd:
	combined := strings.Join(chunks, "\n")
	reply := protocol.ExtractReplyForReq(combined, task.ReqID)
	status := completionhook.StatusCompleted
	if !doneSeen {
		status = completionhook.StatusIncomplete
	}
	if task.Cancelled {
		status = completionhook.StatusCancelled
	}

	var codexLogPath string
	if state.LogPath != "" {
		codexLogPath = state.LogPath
	}

	exitCode := 0
	if !doneSeen {
		exitCode = 2
	}

	result := &ProviderResult{
		ExitCode:     exitCode,
		Reply:        reply,
		ReqID:        task.ReqID,
		SessionKey:   sessionKey,
		DoneSeen:     doneSeen,
		DoneMs:       doneMs,
		AnchorSeen:   anchorSeen,
		AnchorMs:     anchorMs,
		FallbackScan: fallbackScan,
		LogPath:      codexLogPath,
		Status:       status,
	}
	codexWriteLog(fmt.Sprintf(
		"[INFO] done provider=codex req_id=%s exit=%d anchor=%v done=%v",
		task.ReqID, result.ExitCode, result.AnchorSeen, result.DoneSeen,
	))

	replyForHook := reply
	if strings.TrimSpace(replyForHook) == "" {
		replyForHook = completionhook.DefaultReplyForStatus(status, doneSeen)
	}
	codexWriteLog(fmt.Sprintf("[INFO] notify_completion caller=%s status=%s done_seen=%v", req.Caller, status, doneSeen))
	completionhook.NotifyCompletion(completionhook.NotifyParams{
		Provider:       "codex",
		OutputFile:     req.OutputPath,
		Reply:          replyForHook,
		ReqID:          task.ReqID,
		DoneSeen:       doneSeen,
		Status:         status,
		Caller:         req.Caller,
		EmailReqID:     req.EmailReqID,
		EmailMsgID:     req.EmailMsgID,
		EmailFrom:      req.EmailFrom,
		WorkDir:        req.WorkDir,
		CallerPaneID:   req.CallerPaneID,
		CallerTerminal: req.CallerTerminal,
	})

	return result
}

// isLogStale checks if the preferred log is stale relative to the latest log.
func isLogStale(preferred, latest string, thresholdS float64) bool {
	if latest == "" {
		return false
	}
	if preferred == "" {
		return true
	}
	pi, err := os.Stat(preferred)
	if err != nil {
		return true
	}
	if thresholdS <= 0 {
		return false
	}
	li, err := os.Stat(latest)
	if err != nil {
		return true
	}
	return li.ModTime().Sub(pi.ModTime()).Seconds() >= thresholdS
}
