// OpenCode provider adapter for the unified ask daemon.
// Source: claude_code_bridge/lib/askd/adapters/opencode.py
package adapter

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/curdx/curdx-bridge/internal/comm"
	"github.com/curdx/curdx-bridge/internal/completionhook"
	"github.com/curdx/curdx-bridge/internal/processlock"
	"github.com/curdx/curdx-bridge/internal/protocol"
	"github.com/curdx/curdx-bridge/internal/providers"
	"github.com/curdx/curdx-bridge/internal/provprotocol"
	"github.com/curdx/curdx-bridge/internal/runtime"
	"github.com/curdx/curdx-bridge/internal/session"
	"github.com/curdx/curdx-bridge/internal/terminal"
)

// OpenCodeAdapter implements BaseProviderAdapter for the OpenCode provider.
type OpenCodeAdapter struct{}

func (a *OpenCodeAdapter) Key() string                        { return "opencode" }
func (a *OpenCodeAdapter) Spec() providers.ProviderDaemonSpec { return providers.OaskdSpec }
func (a *OpenCodeAdapter) SessionFilename() string            { return ".opencode-session" }
func (a *OpenCodeAdapter) OnStart()                           {}
func (a *OpenCodeAdapter) OnStop()                            {}

func (a *OpenCodeAdapter) LoadSession(workDir string, instance string) (any, error) {
	s := session.LoadOpenCodeSession(workDir, instance)
	if s == nil {
		return nil, nil
	}
	return s, nil
}

func (a *OpenCodeAdapter) ComputeSessionKey(sess any, instance string) string {
	s, ok := sess.(*session.OpenCodeProjectSession)
	if !ok || s == nil {
		return "opencode:unknown"
	}
	return session.ComputeOpenCodeSessionKey(s, instance)
}

func (a *OpenCodeAdapter) HandleException(err error, task *QueuedTask) *ProviderResult {
	return DefaultHandleException("opencode", err, task)
}

func opencodeWriteLog(line string) {
	runtime.WriteLog(runtime.LogPath(providers.OaskdSpec.LogFileName), line)
}

func (a *OpenCodeAdapter) HandleTask(task *QueuedTask) *ProviderResult {
	startedMs := nowMs()
	req := task.Request
	opencodeWriteLog(fmt.Sprintf("[INFO] start provider=opencode req_id=%s work_dir=%s", task.ReqID, req.WorkDir))

	instance := req.Instance
	sess := session.LoadOpenCodeSession(req.WorkDir, instance)
	var sessionKey string
	if sess != nil {
		sessionKey = session.ComputeOpenCodeSessionKey(sess, instance)
	} else {
		sessionKey = "opencode:unknown"
	}

	if sess == nil {
		return &ProviderResult{
			ExitCode:   1,
			Reply:      "No active OpenCode session found for work_dir.",
			ReqID:      task.ReqID,
			SessionKey: sessionKey,
			DoneSeen:   false,
			Status:     completionhook.StatusFailed,
		}
	}

	// Cross-process serialization lock
	var lockTimeout float64
	if req.TimeoutS < 0.0 {
		lockTimeout = 300.0
	} else {
		lockTimeout = math.Min(300.0, math.Max(1.0, req.TimeoutS))
	}
	lock := processlock.NewProviderLock("opencode", lockTimeout, "session:"+sessionKey)
	if !lock.Acquire() {
		return &ProviderResult{
			ExitCode:   1,
			Reply:      "Another OpenCode request is in progress (session lock timeout).",
			ReqID:      task.ReqID,
			SessionKey: sessionKey,
			DoneSeen:   false,
			Status:     completionhook.StatusFailed,
		}
	}
	defer lock.Release()

	return a.handleTaskLocked(task, sess, sessionKey, startedMs)
}

func (a *OpenCodeAdapter) handleTaskLocked(task *QueuedTask, sess *session.OpenCodeProjectSession, sessionKey string, startedMs int64) *ProviderResult {
	req := task.Request

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

	var opts []comm.OpenCodeOption
	if filter := sess.OpenCodeSessionIDFilter(); filter != "" {
		opts = append(opts, comm.WithSessionIDFilter(filter))
	}
	logReader := comm.NewOpenCodeLogReader("", sess.WorkDir(), "global", opts...)
	state := logReader.CaptureState()

	// Update session binding with detected session info
	sess.UpdateOpenCodeBinding(state.SessionID, logReader.ProjectID)

	prompt := provprotocol.WrapOpenCodePrompt(req.Message, task.ReqID)
	if err := backend.SendText(paneID, prompt); err != nil {
		opencodeWriteLog(fmt.Sprintf("[ERROR] SendText failed req_id=%s: %v", task.ReqID, err))
		return &ProviderResult{
			ExitCode:   1,
			Reply:      fmt.Sprintf("failed to send text: %v", err),
			ReqID:      task.ReqID,
			SessionKey: sessionKey,
			DoneSeen:   false,
			Status:     completionhook.StatusFailed,
		}
	}

	// Async mode: timeout_s == 0 means fire-and-forget
	if req.TimeoutS == 0.0 {
		v := int(nowMs() - startedMs)
		return &ProviderResult{
			ExitCode:   0,
			Reply:      "",
			ReqID:      task.ReqID,
			SessionKey: sessionKey,
			DoneSeen:   true,
			DoneMs:     &v,
			Status:     completionhook.StatusCompleted,
		}
	}

	var deadline *time.Time
	if req.TimeoutS >= 0.0 {
		d := time.Now().Add(time.Duration(req.TimeoutS * float64(time.Second)))
		deadline = &d
	}

	var chunks []string
	doneSeen := false
	var doneMs *int

	paneCheckInterval := envFloatDefault("CURDX_OASKD_PANE_CHECK_INTERVAL", 2.0)
	lastPaneCheck := time.Now()

	for {
		// Check for cancellation
		if task.CancelEvent != nil {
			select {
			case <-task.CancelEvent:
				opencodeWriteLog(fmt.Sprintf("[INFO] Task cancelled during wait loop: req_id=%s", task.ReqID))
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
			waitStep = min(remaining, 1*time.Second)
		} else {
			waitStep = 1 * time.Second
		}

		if time.Since(lastPaneCheck).Seconds() >= paneCheckInterval {
			alive := backend.IsAlive(paneID)
			if !alive {
				opencodeWriteLog(fmt.Sprintf("[ERROR] Pane %s died during request req_id=%s", paneID, task.ReqID))
				return &ProviderResult{
					ExitCode:   1,
					Reply:      "OpenCode pane died during request",
					ReqID:      task.ReqID,
					SessionKey: sessionKey,
					DoneSeen:   false,
					Status:     completionhook.StatusFailed,
				}
			}
			lastPaneCheck = time.Now()
		}

		reply, newState := logReader.WaitForMessage(state, waitStep)
		state = newState
		if reply == "" {
			continue
		}
		chunks = append(chunks, reply)
		combined := strings.Join(chunks, "\n")
		if protocol.IsDoneText(combined, task.ReqID) {
			doneSeen = true
			v := int(nowMs() - startedMs)
			doneMs = &v
			break
		}
	}

loopEnd:
	combined := strings.Join(chunks, "\n")
	finalReply := protocol.StripDoneText(combined, task.ReqID)
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
		Provider:       "opencode",
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
		ExitCode:   exitCode,
		Reply:      finalReply,
		ReqID:      task.ReqID,
		SessionKey: sessionKey,
		DoneSeen:   doneSeen,
		DoneMs:     doneMs,
		Status:     status,
	}
	opencodeWriteLog(fmt.Sprintf("[INFO] done provider=opencode req_id=%s exit=%d", task.ReqID, result.ExitCode))
	return result
}
