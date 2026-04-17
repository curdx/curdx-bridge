// Gemini provider adapter for the unified ask daemon.
// Source: claude_code_bridge/lib/askd/adapters/gemini.py
package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/curdx/curdx-bridge/internal/comm"
	"github.com/curdx/curdx-bridge/internal/completionhook"
	"github.com/curdx/curdx-bridge/internal/protocol"
	"github.com/curdx/curdx-bridge/internal/providers"
	"github.com/curdx/curdx-bridge/internal/provprotocol"
	"github.com/curdx/curdx-bridge/internal/runtime"
	"github.com/curdx/curdx-bridge/internal/session"
	"github.com/curdx/curdx-bridge/internal/terminal"
)

// GeminiAdapter implements BaseProviderAdapter for the Gemini provider.
type GeminiAdapter struct{}

func (a *GeminiAdapter) Key() string                        { return "gemini" }
func (a *GeminiAdapter) Spec() providers.ProviderDaemonSpec { return providers.GaskdSpec }
func (a *GeminiAdapter) SessionFilename() string            { return ".gemini-session" }
func (a *GeminiAdapter) OnStart()                           {}
func (a *GeminiAdapter) OnStop()                            {}

func (a *GeminiAdapter) LoadSession(workDir string, instance string) (any, error) {
	s := session.LoadGeminiSession(workDir, instance)
	if s == nil {
		return nil, nil
	}
	return s, nil
}

func (a *GeminiAdapter) ComputeSessionKey(sess any, instance string) string {
	s, ok := sess.(*session.GeminiProjectSession)
	if !ok || s == nil {
		return "gemini:unknown"
	}
	return session.ComputeGeminiSessionKey(s, instance)
}

func (a *GeminiAdapter) HandleException(err error, task *QueuedTask) *ProviderResult {
	return DefaultHandleException("gemini", err, task)
}

func geminiWriteLog(line string) {
	runtime.WriteLog(runtime.LogPath(providers.GaskdSpec.LogFileName), line)
}

// isCancelText checks if text indicates a Gemini request cancellation.
func isCancelText(text string) bool {
	s := strings.TrimSpace(strings.ToLower(text))
	if s == "" {
		return false
	}
	if strings.Contains(s, "request cancelled") || strings.Contains(s, "request canceled") {
		return true
	}
	return false
}

// readSessionMessages reads and parses messages from a Gemini session JSON file.
func readSessionMessages(sessionPath string) []map[string]any {
	for attempt := range 10 {
		data, err := os.ReadFile(sessionPath)
		if err != nil {
			return nil
		}
		var obj map[string]any
		if err := json.Unmarshal(data, &obj); err != nil {
			if attempt < 9 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return nil
		}
		messages, ok := obj["messages"].([]any)
		if !ok {
			return []map[string]any{}
		}
		var result []map[string]any
		for _, raw := range messages {
			if m, ok := raw.(map[string]any); ok {
				result = append(result, m)
			}
		}
		return result
	}
	return nil
}

// cancelAppliesToReq checks if a cancel event at cancelIndex applies to our request.
func cancelAppliesToReq(messages []map[string]any, cancelIndex int, reqID string) bool {
	needle := fmt.Sprintf("CURDX_REQ_ID: %s", reqID)
	for j := cancelIndex - 1; j >= 0; j-- {
		msg := messages[j]
		msgType, _ := msg["type"].(string)
		if msgType != "user" {
			continue
		}
		content, _ := msg["content"].(string)
		return strings.Contains(content, needle)
	}
	return false
}

// detectRequestCancelled checks the session file for cancellation events.
func detectRequestCancelled(sessionPath string, fromIndex int, reqID string) bool {
	if fromIndex < 0 {
		fromIndex = 0
	}
	messages := readSessionMessages(sessionPath)
	if messages == nil {
		return false
	}
	start := min(fromIndex, len(messages))
	for i := start; i < len(messages); i++ {
		msg := messages[i]
		msgType, _ := msg["type"].(string)
		if msgType != "info" {
			continue
		}
		content, _ := msg["content"].(string)
		if !isCancelText(content) {
			continue
		}
		if cancelAppliesToReq(messages, i, reqID) {
			return true
		}
	}
	return false
}

func (a *GeminiAdapter) HandleTask(task *QueuedTask) *ProviderResult {
	startedMs := nowMs()
	req := task.Request
	geminiWriteLog(fmt.Sprintf("[INFO] start provider=gemini req_id=%s work_dir=%s", task.ReqID, req.WorkDir))

	instance := req.Instance
	sess := session.LoadGeminiSession(req.WorkDir, instance)
	var sessionKey string
	if sess != nil {
		sessionKey = session.ComputeGeminiSessionKey(sess, instance)
	} else {
		sessionKey = "gemini:unknown"
	}

	if sess == nil {
		return &ProviderResult{
			ExitCode:   1,
			Reply:      "No active Gemini session found for work_dir.",
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

	logReader := comm.NewGeminiLogReader("", sess.WorkDir())
	if sess.GeminiSessionPath() != "" {
		logReader.SetPreferredSession(sess.GeminiSessionPath())
	}
	state := logReader.CaptureState()

	prompt := provprotocol.WrapGeminiPrompt(req.Message, task.ReqID)
	if err := backend.SendText(paneID, prompt); err != nil {
		geminiWriteLog(fmt.Sprintf("[ERROR] SendText failed req_id=%s: %v", task.ReqID, err))
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

	doneSeen := false
	var doneMs *int
	latestReply := ""
	requestCancelled := false

	paneCheckInterval := envFloatDefault("CURDX_GASKD_PANE_CHECK_INTERVAL", 2.0)
	lastPaneCheck := time.Now()

	// Idle-timeout
	idleTimeout := envFloatDefault("CURDX_GEMINI_IDLE_TIMEOUT", 15.0)
	lastReplySnapshot := ""
	lastReplyChangedAt := time.Now()

	for {
		// Check for cancellation
		if task.CancelEvent != nil {
			select {
			case <-task.CancelEvent:
				geminiWriteLog(fmt.Sprintf("[INFO] Task cancelled during wait loop: req_id=%s", task.ReqID))
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
				geminiWriteLog(fmt.Sprintf("[ERROR] Pane %s died during request req_id=%s", paneID, task.ReqID))
				return &ProviderResult{
					ExitCode:   1,
					Reply:      "Gemini pane died during request",
					ReqID:      task.ReqID,
					SessionKey: sessionKey,
					DoneSeen:   false,
					Status:     completionhook.StatusFailed,
				}
			}
			lastPaneCheck = time.Now()
		}

		scanFrom := max(state.MsgCount, 0)
		prevSessionPath := state.SessionPath

		reply, newState := logReader.WaitForMessage(state, waitStep)
		state = newState

		// Detect cancellation
		currentCount := state.MsgCount
		sessionPath := state.SessionPath
		scanFromI := scanFrom
		if sessionPath != "" && prevSessionPath != "" && sessionPath != prevSessionPath {
			scanFromI = 0
		}
		if sessionPath != "" && currentCount > scanFromI {
			if detectRequestCancelled(sessionPath, scanFromI, task.ReqID) {
				geminiWriteLog(fmt.Sprintf("[WARN] Gemini request cancelled req_id=%s", task.ReqID))
				requestCancelled = true
				latestReply = "Gemini request cancelled."
				break
			}
		}

		if reply == "" {
			continue
		}
		latestReply = reply
		if protocol.IsDoneText(latestReply, task.ReqID) {
			doneSeen = true
			v := int(nowMs() - startedMs)
			doneMs = &v
			break
		}

		// Idle-timeout
		if latestReply != lastReplySnapshot {
			lastReplySnapshot = latestReply
			lastReplyChangedAt = time.Now()
		} else if latestReply != "" && time.Since(lastReplyChangedAt).Seconds() >= idleTimeout {
			geminiWriteLog(fmt.Sprintf(
				"[WARN] Gemini reply idle for %.0fs without CURDX_DONE, accepting as complete req_id=%s",
				idleTimeout, task.ReqID,
			))
			doneSeen = true
			v := int(nowMs() - startedMs)
			doneMs = &v
			break
		}
	}

loopEnd:
	finalReply := provprotocol.ExtractReplyStandard(latestReply, task.ReqID, protocol.StripDoneText)
	status := completionhook.StatusCompleted
	if !doneSeen {
		status = completionhook.StatusIncomplete
	}
	if requestCancelled || task.Cancelled {
		status = completionhook.StatusCancelled
	}

	replyForHook := finalReply
	if strings.TrimSpace(replyForHook) == "" {
		replyForHook = completionhook.DefaultReplyForStatus(status, doneSeen)
	}
	completionhook.NotifyCompletion(completionhook.NotifyParams{
		Provider:       "gemini",
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
	geminiWriteLog(fmt.Sprintf("[INFO] done provider=gemini req_id=%s exit=%d", task.ReqID, result.ExitCode))
	return result
}
