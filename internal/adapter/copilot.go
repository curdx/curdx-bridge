// Copilot provider adapter for the unified ask daemon.
// Source: claude_code_bridge/lib/askd/adapters/copilot.py
package adapter

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropics/curdx-bridge/internal/comm"
	"github.com/anthropics/curdx-bridge/internal/completionhook"
	"github.com/anthropics/curdx-bridge/internal/protocol"
	"github.com/anthropics/curdx-bridge/internal/provprotocol"
	"github.com/anthropics/curdx-bridge/internal/providers"
	"github.com/anthropics/curdx-bridge/internal/runtime"
	"github.com/anthropics/curdx-bridge/internal/session"
	"github.com/anthropics/curdx-bridge/internal/terminal"
)

// CopilotAdapter implements BaseProviderAdapter for the Copilot provider.
type CopilotAdapter struct{}

func (a *CopilotAdapter) Key() string                        { return "copilot" }
func (a *CopilotAdapter) Spec() providers.ProviderDaemonSpec { return providers.HaskdSpec }
func (a *CopilotAdapter) SessionFilename() string            { return ".copilot-session" }
func (a *CopilotAdapter) OnStart()                           {}
func (a *CopilotAdapter) OnStop()                            {}

func (a *CopilotAdapter) LoadSession(workDir string, instance string) (any, error) {
	s := session.LoadCopilotSession(workDir, instance)
	if s == nil {
		return nil, nil
	}
	return s, nil
}

func (a *CopilotAdapter) ComputeSessionKey(sess any, instance string) string {
	s, ok := sess.(*session.CopilotProjectSession)
	if !ok || s == nil {
		return "copilot:unknown"
	}
	return session.ComputeCopilotSessionKey(s, instance)
}

func (a *CopilotAdapter) HandleException(err error, task *QueuedTask) *ProviderResult {
	return DefaultHandleException("copilot", err, task)
}

func copilotWriteLog(line string) {
	runtime.WriteLog(runtime.LogPath(providers.HaskdSpec.LogFileName), line)
}

// resolveCopilotPaneLogPath resolves the pane log path for a Copilot session.
func resolveCopilotPaneLogPath(sess *session.CopilotProjectSession) string {
	if raw, ok := sess.Data["pane_log_path"]; ok && raw != nil {
		if s, ok := raw.(string); ok && s != "" {
			return expandHome(s)
		}
	}
	if runtimeDir := sess.RuntimeDir(); runtimeDir != "" {
		return filepath.Join(runtimeDir, "pane.log")
	}
	return ""
}

func (a *CopilotAdapter) HandleTask(task *QueuedTask) *ProviderResult {
	startedMs := nowMs()
	req := task.Request
	copilotWriteLog(fmt.Sprintf("[INFO] start provider=copilot req_id=%s work_dir=%s", task.ReqID, req.WorkDir))

	instance := req.Instance
	sess := session.LoadCopilotSession(req.WorkDir, instance)
	var sessionKey string
	if sess != nil {
		sessionKey = session.ComputeCopilotSessionKey(sess, instance)
	} else {
		sessionKey = "copilot:unknown"
	}

	if sess == nil {
		return &ProviderResult{
			ExitCode:   1,
			Reply:      "No active Copilot session found for work_dir.",
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

	// Copilot uses pane-log based communication (no JSONL session logs)
	paneLogPath := resolveCopilotPaneLogPath(sess)

	logReader := comm.NewCopilotLogReader(sess.WorkDir(), paneLogPath)
	state := logReader.CaptureState()

	prompt := provprotocol.WrapCopilotPrompt(req.Message, task.ReqID)
	if err := backend.SendText(paneID, prompt); err != nil {
		copilotWriteLog(fmt.Sprintf("[ERROR] SendText failed req_id=%s: %v", task.ReqID, err))
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
	fallbackScan := false
	var anchorMs *int
	doneSeen := false
	var doneMs *int

	var anchorGraceDeadline time.Time
	graceLimit15 := time.Now().Add(1500 * time.Millisecond)
	if deadline != nil && deadline.Before(graceLimit15) {
		anchorGraceDeadline = *deadline
	} else {
		anchorGraceDeadline = graceLimit15
	}

	var anchorCollectGrace time.Time
	graceLimit20 := time.Now().Add(2000 * time.Millisecond)
	if deadline != nil && deadline.Before(graceLimit20) {
		anchorCollectGrace = *deadline
	} else {
		anchorCollectGrace = graceLimit20
	}

	rebounded := false
	tailBytes := int64(envIntDefault("CCB_HASKD_REBIND_TAIL_BYTES", 2*1024*1024))
	paneCheckInterval := envFloatDefault("CCB_HASKD_PANE_CHECK_INTERVAL", 2.0)
	lastPaneCheck := time.Now()

	for {
		// Check for cancellation
		if task.CancelEvent != nil {
			select {
			case <-task.CancelEvent:
				copilotWriteLog(fmt.Sprintf("[INFO] Task cancelled during wait loop: req_id=%s", task.ReqID))
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
			waitStep = 500 * time.Millisecond
			if remaining < waitStep {
				waitStep = remaining
			}
		} else {
			waitStep = 500 * time.Millisecond
		}

		if time.Since(lastPaneCheck).Seconds() >= paneCheckInterval {
			alive := backend.IsAlive(paneID)
			if !alive {
				copilotWriteLog(fmt.Sprintf("[ERROR] Pane %s died during request req_id=%s", paneID, task.ReqID))
				return &ProviderResult{
					ExitCode:     1,
					Reply:        "Copilot pane died during request",
					ReqID:        task.ReqID,
					SessionKey:   sessionKey,
					DoneSeen:     false,
					AnchorSeen:   anchorSeen,
					FallbackScan: fallbackScan,
					AnchorMs:     anchorMs,
					Status:       completionhook.StatusFailed,
				}
			}
			lastPaneCheck = time.Now()
		}

		events, newState := logReader.WaitForEvents(state, waitStep)
		state = newState

		if len(events) == 0 {
			if !rebounded && !anchorSeen && time.Now().After(anchorGraceDeadline) {
				logReader = comm.NewCopilotLogReader(sess.WorkDir(), paneLogPath)
				plp, offset := tailStateForPaneLog(paneLogPath, tailBytes)
				state = comm.PaneLogState{PaneLogPath: plp, Offset: offset}
				fallbackScan = true
				rebounded = true
			}
			continue
		}

		for _, ev := range events {
			if ev.Role == "user" {
				if strings.Contains(ev.Text, fmt.Sprintf("%s %s", protocol.REQIDPrefix, task.ReqID)) {
					anchorSeen = true
					if anchorMs == nil {
						v := int(nowMs() - startedMs)
						anchorMs = &v
					}
				}
				continue
			}
			if ev.Role != "assistant" {
				continue
			}
			if !anchorSeen && time.Now().Before(anchorCollectGrace) {
				continue
			}
			chunks = append(chunks, ev.Text)
			combined := strings.Join(chunks, "\n")
			if protocol.IsDoneText(combined, task.ReqID) {
				doneSeen = true
				v := int(nowMs() - startedMs)
				doneMs = &v
				break
			}
		}

		if doneSeen {
			break
		}
	}

loopEnd:
	combined := strings.Join(chunks, "\n")
	finalReply := provprotocol.ExtractReplyStandard(combined, task.ReqID, protocol.StripDoneText)
	status := completionhook.StatusCompleted
	if !doneSeen {
		status = completionhook.StatusIncomplete
	}
	if task.Cancelled {
		status = completionhook.StatusCancelled
	}

	replyForHook := finalReply
	if strings.TrimSpace(replyForHook) == "" {
		replyForHook = completionhook.DefaultReplyForStatus(status, doneSeen)
	}
	completionhook.NotifyCompletion(completionhook.NotifyParams{
		Provider:       "copilot",
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

	exitCode := 0
	if !doneSeen {
		exitCode = 2
	}

	result := &ProviderResult{
		ExitCode:     exitCode,
		Reply:        finalReply,
		ReqID:        task.ReqID,
		SessionKey:   sessionKey,
		DoneSeen:     doneSeen,
		DoneMs:       doneMs,
		AnchorSeen:   anchorSeen,
		AnchorMs:     anchorMs,
		FallbackScan: fallbackScan,
		Status:       status,
	}
	copilotWriteLog(fmt.Sprintf("[INFO] done provider=copilot req_id=%s exit=%d", task.ReqID, result.ExitCode))
	return result
}
