// Claude provider adapter for the unified ask daemon.
// Source: claude_code_bridge/lib/askd/adapters/claude.py
package adapter

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/curdx/curdx-bridge/internal/comm"
	"github.com/curdx/curdx-bridge/internal/completionhook"
	"github.com/curdx/curdx-bridge/internal/protocol"
	"github.com/curdx/curdx-bridge/internal/provprotocol"
	"github.com/curdx/curdx-bridge/internal/providers"
	"github.com/curdx/curdx-bridge/internal/runtime"
	"github.com/curdx/curdx-bridge/internal/session"
	"github.com/curdx/curdx-bridge/internal/terminal"
)

// ClaudeAdapter implements BaseProviderAdapter for the Claude provider.
type ClaudeAdapter struct{}

func (a *ClaudeAdapter) Key() string                        { return "claude" }
func (a *ClaudeAdapter) Spec() providers.ProviderDaemonSpec { return providers.LaskdSpec }
func (a *ClaudeAdapter) SessionFilename() string            { return ".claude-session" }

func (a *ClaudeAdapter) OnStart() {
	// In the Python version, this initialises the session registry and file watcher.
	// The Go equivalent will be wired up externally via the paneregistry package.
	claudeWriteLog("[INFO] claude adapter started")
}

func (a *ClaudeAdapter) OnStop() {
	// Stop log watcher if running.
	claudeWriteLog("[INFO] claude adapter stopped")
}

func (a *ClaudeAdapter) LoadSession(workDir string, instance string) (any, error) {
	s := session.LoadClaudeSession(workDir, instance)
	if s == nil {
		return nil, nil
	}
	return s, nil
}

func (a *ClaudeAdapter) ComputeSessionKey(sess any, instance string) string {
	s, ok := sess.(*session.ClaudeProjectSession)
	if !ok || s == nil {
		return "claude:unknown"
	}
	return session.ComputeClaudeSessionKey(s, instance)
}

func (a *ClaudeAdapter) HandleException(err error, task *QueuedTask) *ProviderResult {
	return DefaultHandleException("claude", err, task)
}

func claudeWriteLog(line string) {
	runtime.WriteLog(runtime.LogPath(providers.LaskdSpec.LogFileName), line)
}

func (a *ClaudeAdapter) HandleTask(task *QueuedTask) *ProviderResult {
	startedMs := nowMs()
	req := task.Request
	claudeWriteLog(fmt.Sprintf("[INFO] start provider=claude req_id=%s work_dir=%s", task.ReqID, req.WorkDir))

	instance := req.Instance
	sess := session.LoadClaudeSession(req.WorkDir, instance)
	var sessionKey string
	if sess != nil {
		sessionKey = session.ComputeClaudeSessionKey(sess, instance)
	} else {
		sessionKey = "claude:unknown"
	}

	if sess == nil {
		return &ProviderResult{
			ExitCode:   1,
			Reply:      "No active Claude session found for work_dir.",
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

	backend := terminal.GetBackendForSession(sess.GetDataSnapshot())
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

	var deadline *time.Time
	if req.TimeoutS >= 0.0 {
		d := time.Now().Add(time.Duration(req.TimeoutS * float64(time.Second)))
		deadline = &d
	}

	logReader := comm.NewClaudeLogReader("", sess.WorkDir())
	if sess.ClaudeSessionPath() != "" {
		logReader.SetPreferredSession(sess.ClaudeSessionPath())
	}
	state := logReader.CaptureState()

	var prompt string
	if req.NoWrap {
		prompt = req.Message
	} else {
		prompt = provprotocol.WrapClaudePrompt(req.Message, task.ReqID)
	}
	if err := backend.SendText(paneID, prompt); err != nil {
		claudeWriteLog(fmt.Sprintf("[ERROR] SendText failed req_id=%s: %v", task.ReqID, err))
		return &ProviderResult{
			ExitCode:   1,
			Reply:      fmt.Sprintf("failed to send text: %v", err),
			ReqID:      task.ReqID,
			SessionKey: sessionKey,
			DoneSeen:   false,
			Status:     completionhook.StatusFailed,
		}
	}

	// Use structured Claude session logs only
	result := a.waitForResponse(task, sess, sessionKey, startedMs, logReader, state, backend, paneID, deadline)
	result.Reply = claudePostprocessReply(req, result.Reply)
	a.finalizeResult(result, req, task)
	return result
}

func (a *ClaudeAdapter) finalizeResult(result *ProviderResult, req ProviderRequest, task *QueuedTask) {
	claudeWriteLog(fmt.Sprintf("[INFO] done provider=claude req_id=%s exit=%d", result.ReqID, result.ExitCode))

	replyForHook := result.Reply
	status := result.Status
	if status == "" {
		if result.DoneSeen {
			status = completionhook.StatusCompleted
		} else {
			status = completionhook.StatusIncomplete
		}
	}
	if task.Cancelled {
		claudeWriteLog(fmt.Sprintf("[WARN] Task cancelled, sending cancellation completion hook: req_id=%s", task.ReqID))
		status = completionhook.StatusCancelled
	}
	if strings.TrimSpace(replyForHook) == "" {
		replyForHook = completionhook.DefaultReplyForStatus(status, result.DoneSeen)
	}

	claudeWriteLog(fmt.Sprintf(
		"[INFO] notify_completion caller=%s status=%s done_seen=%v email_req_id=%s",
		req.Caller, status, result.DoneSeen, req.EmailReqID,
	))
	completionhook.NotifyCompletion(completionhook.NotifyParams{
		Provider:       "claude",
		OutputFile:     req.OutputPath,
		Reply:          replyForHook,
		ReqID:          result.ReqID,
		DoneSeen:       result.DoneSeen,
		Status:         status,
		Caller:         req.Caller,
		EmailReqID:     req.EmailReqID,
		EmailMsgID:     req.EmailMsgID,
		EmailFrom:      req.EmailFrom,
		WorkDir:        req.WorkDir,
		CallerPaneID:   req.CallerPaneID,
		CallerTerminal: req.CallerTerminal,
	})
}

func (a *ClaudeAdapter) waitForResponse(
	task *QueuedTask, sess *session.ClaudeProjectSession, sessionKey string,
	startedMs int64, logReader *comm.ClaudeLogReader, state comm.ClaudeLogState,
	backend terminal.TerminalBackend, paneID string, deadline *time.Time,
) *ProviderResult {
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
	tailBytes := int64(envIntDefault("CURDX_LASKD_REBIND_TAIL_BYTES", 2*1024*1024))
	paneCheckInterval := envFloatDefault("CURDX_LASKD_PANE_CHECK_INTERVAL", 2.0)
	lastPaneCheck := time.Now()

	for {
		if task.CancelEvent != nil {
			select {
			case <-task.CancelEvent:
				claudeWriteLog(fmt.Sprintf("[INFO] Task cancelled during wait loop: req_id=%s", task.ReqID))
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
				claudeWriteLog(fmt.Sprintf("[ERROR] Pane %s died req_id=%s", paneID, task.ReqID))
				return &ProviderResult{
					ExitCode:     1,
					Reply:        "Claude pane died during request",
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
				logReader = comm.NewClaudeLogReader("", sess.WorkDir(), comm.WithSessionsIndex(false))
				logHint := logReader.CurrentSessionPath()
				sessionPath, offset := tailStateForJSONL(logHint, tailBytes)
				state = comm.ClaudeLogState{SessionPath: sessionPath, Offset: offset}
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
	finalReply := provprotocol.ExtractReplyForClaude(combined, task.ReqID, protocol.StripDoneText)

	status := completionhook.StatusCompleted
	if !doneSeen {
		if task.Cancelled {
			status = completionhook.StatusCancelled
		} else {
			status = completionhook.StatusIncomplete
		}
	}

	exitCode := 0
	if !doneSeen {
		exitCode = 2
	}

	return &ProviderResult{
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
}

// ---------------------------------------------------------------------------
// Claude postprocessing logic
// Source: claude_code_bridge/lib/askd/adapters/claude.py postprocess helpers
// ---------------------------------------------------------------------------

var boxTableChars = map[rune]bool{
	'┌': true, '┬': true, '┐': true, '├': true, '┼': true,
	'┤': true, '└': true, '┴': true, '┘': true, '│': true, '─': true,
}

func wantsTripletFences(message string) bool {
	msg := strings.ToLower(message)
	if strings.Contains(msg, "python") && strings.Contains(msg, "json") && strings.Contains(msg, "yaml") {
		return strings.Contains(msg, "code block") || strings.Contains(message, "代码块")
	}
	return false
}

func wantsBashFence(message string) bool {
	msg := strings.ToLower(message)
	if strings.Contains(msg, "bash") {
		return strings.Contains(msg, "code block") || strings.Contains(message, "代码块")
	}
	return false
}

func wantsTextFence(message string) bool {
	msg := strings.ToLower(message)
	if strings.Contains(msg, "```text") || strings.Contains(msg, "text") {
		return strings.Contains(msg, "code block") || strings.Contains(message, "代码块")
	}
	return false
}

func wantsReleaseNotes(message string) bool {
	msg := strings.ToLower(message)
	if !strings.Contains(msg, "release notes") {
		return false
	}
	return strings.Contains(msg, "summary") && strings.Contains(msg, "item") &&
		strings.Contains(msg, "risk") && strings.Contains(msg, "action")
}

func looksLikeReleaseNotesReply(reply string) bool {
	if reply == "" {
		return false
	}
	text := strings.ToLower(reply)
	return strings.Contains(text, "release notes") && strings.Contains(text, "summary:")
}

func wantsABCSections(message string) bool {
	msg := strings.ToLower(message)
	return strings.Contains(msg, "## a") && strings.Contains(msg, "## b") && strings.Contains(msg, "## c")
}

func wantsSection10(message string) bool {
	msg := strings.ToLower(message)
	return strings.Contains(msg, "### section") && strings.Contains(msg, "1..10")
}

func hasFence(reply string) bool {
	return strings.Contains(reply, "```")
}

func isBoxTableLine(line string) bool {
	for _, ch := range line {
		if boxTableChars[ch] {
			return true
		}
	}
	return false
}

func shouldFixBoxTable(message, reply string) bool {
	if reply == "" {
		return false
	}
	if !isBoxTableLine(reply) {
		return false
	}
	msg := strings.ToLower(message)
	if !strings.Contains(msg, "markdown") {
		return false
	}
	return strings.Contains(msg, "table") || strings.Contains(message, "表格")
}

func convertBoxTableToMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return text
	}
	var start, end *int
	for i, ln := range lines {
		if isBoxTableLine(ln) {
			if start == nil {
				v := i
				start = &v
			}
			v := i
			end = &v
			continue
		}
		if start != nil {
			if strings.TrimSpace(ln) == "" {
				v := i
				end = &v
				continue
			}
			break
		}
	}
	if start == nil || end == nil {
		return text
	}

	block := lines[*start : *end+1]
	var rows [][]string
	for _, ln := range block {
		if !strings.Contains(ln, "│") {
			continue
		}
		raw := strings.TrimSpace(ln)
		if raw == "" {
			continue
		}
		raw = strings.Trim(raw, "│")
		parts := strings.Split(raw, "│")
		var trimmed []string
		for _, p := range parts {
			trimmed = append(trimmed, strings.TrimSpace(p))
		}
		allEmpty := true
		for _, p := range trimmed {
			if p != "" {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			continue
		}
		rows = append(rows, trimmed)
	}
	if len(rows) == 0 {
		return text
	}

	header := rows[0]
	colCount := len(header)
	if colCount == 0 {
		return text
	}
	for i := range header {
		if header[i] == "" {
			header[i] = ""
		}
	}
	sep := make([]string, colCount)
	for i := range sep {
		sep[i] = "---"
	}
	out := []string{
		"| " + strings.Join(header, " | ") + " |",
		"| " + strings.Join(sep, " | ") + " |",
	}
	for _, row := range rows[1:] {
		for len(row) < colCount {
			row = append(row, "")
		}
		if len(row) > colCount {
			row = row[:colCount]
		}
		out = append(out, "| "+strings.Join(row, " | ")+" |")
	}

	rebuilt := append(lines[:*start], out...)
	rebuilt = append(rebuilt, lines[*end+1:]...)
	return strings.TrimRight(strings.Join(rebuilt, "\n"), " \t\n\r")
}

func fixTripletFences(reply string) string {
	lines := strings.Split(reply, "\n")
	if hasFence(reply) {
		pyCount := strings.Count(reply, "```python")
		jsonCount := strings.Count(reply, "```json")
		yamlCount := strings.Count(reply, "```yaml")
		if pyCount == 1 && jsonCount == 1 && yamlCount == 1 {
			return reply
		}
		var filtered []string
		for _, ln := range lines {
			if !strings.HasPrefix(strings.TrimSpace(ln), "```") {
				filtered = append(filtered, ln)
			}
		}
		lines = filtered
	}

	firstIdx := func(pred func(string) bool) *int {
		for i, ln := range lines {
			if pred(ln) {
				return &i
			}
		}
		return nil
	}

	pyStart := firstIdx(func(ln string) bool { return strings.HasPrefix(strings.TrimLeft(ln, " \t"), "def ") })
	jsonStart := firstIdx(func(ln string) bool {
		t := strings.TrimLeft(ln, " \t")
		return strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")
	})
	yamlStart := firstIdx(func(ln string) bool {
		t := strings.TrimSpace(ln)
		return strings.HasPrefix(t, "name:") || strings.HasPrefix(t, "version:")
	})

	type segment struct {
		tag   string
		start int
	}
	var segments []segment
	if pyStart != nil {
		segments = append(segments, segment{"python", *pyStart})
	}
	if jsonStart != nil {
		segments = append(segments, segment{"json", *jsonStart})
	}
	if yamlStart != nil {
		segments = append(segments, segment{"yaml", *yamlStart})
	}
	// Sort by start position
	for i := 0; i < len(segments); i++ {
		for j := i + 1; j < len(segments); j++ {
			if segments[j].start < segments[i].start {
				segments[i], segments[j] = segments[j], segments[i]
			}
		}
	}

	if len(segments) == 0 {
		return reply
	}

	var outBlocks []string
	for idx, seg := range segments {
		end := len(lines)
		if idx+1 < len(segments) {
			end = segments[idx+1].start
		}
		segLines := lines[seg.start:end]
		for len(segLines) > 0 && strings.TrimSpace(segLines[0]) == "" {
			segLines = segLines[1:]
		}
		for len(segLines) > 0 && strings.TrimSpace(segLines[len(segLines)-1]) == "" {
			segLines = segLines[:len(segLines)-1]
		}
		text := strings.TrimSpace(strings.Join(segLines, "\n"))
		if text == "" {
			continue
		}
		outBlocks = append(outBlocks, fmt.Sprintf("```%s\n%s\n```", seg.tag, text))
	}
	return strings.TrimRight(strings.Join(outBlocks, "\n\n"), " \t\n\r")
}

func fixBashFence(reply string) string {
	if hasFence(reply) {
		return reply
	}
	lines := strings.Split(reply, "\n")
	if len(lines) == 0 {
		return reply
	}
	var start *int
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			start = &i
			break
		}
	}
	if start == nil {
		return reply
	}
	var script []string
	i := *start
	for i < len(lines) {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			break
		}
		if len(script) > 0 && (strings.HasPrefix(strings.TrimLeft(line, " \t"), "[") || strings.HasPrefix(strings.TrimLeft(line, " \t"), "{")) {
			break
		}
		script = append(script, line)
		i++
	}
	if len(script) == 0 {
		return reply
	}
	rest := lines[i:]
	for len(rest) > 0 && strings.TrimSpace(rest[0]) == "" {
		rest = rest[1:]
	}
	out := []string{"```bash"}
	out = append(out, script...)
	out = append(out, "```")
	if len(rest) > 0 {
		out = append(out, "")
		out = append(out, rest...)
	}
	return strings.TrimRight(strings.Join(out, "\n"), " \t\n\r")
}

func fixTextFence(reply string) string {
	if hasFence(reply) {
		return reply
	}
	body := strings.TrimSpace(reply)
	if body == "" {
		return reply
	}
	return fmt.Sprintf("```text\n%s\n```", body)
}

func fixABCSections(reply string) string {
	lines := strings.Split(reply, "\n")
	for i, line := range lines {
		stripped := strings.TrimSpace(line)
		if stripped == "A" || stripped == "B" || stripped == "C" {
			lines[i] = "## " + stripped
		}
	}

	var out []string
	i := 0
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "## ") {
			out = append(out, line)
			i++
			var bullets []string
			for i < len(lines) {
				nxt := strings.TrimSpace(lines[i])
				if strings.HasPrefix(nxt, "## ") {
					break
				}
				if strings.HasPrefix(nxt, "- ") {
					bullets = append(bullets, nxt)
				}
				i++
			}
			if len(bullets) > 2 {
				bullets = bullets[:2]
			}
			out = append(out, bullets...)
			continue
		}
		i++
	}
	return strings.TrimRight(strings.Join(out, "\n"), " \t\n\r")
}

func splitToTwoLines(text string) (string, string) {
	if text == "" {
		return "", ""
	}
	for _, sep := range []string{"。", ".", "！", "!", "？", "?"} {
		idx := strings.Index(text, sep)
		if idx != -1 && idx+len(sep) < len(text) {
			first := strings.TrimSpace(text[:idx+len(sep)])
			second := strings.TrimSpace(text[idx+len(sep):])
			if second != "" {
				return first, second
			}
		}
	}
	words := strings.Fields(text)
	if len(words) >= 2 {
		mid := len(words) / 2
		return strings.TrimSpace(strings.Join(words[:mid], " ")), strings.TrimSpace(strings.Join(words[mid:], " "))
	}
	mid := len(text) / 2
	if mid < 1 {
		mid = 1
	}
	return strings.TrimSpace(text[:mid]), strings.TrimSpace(text[mid:])
}

var sectionNumRE = regexp.MustCompile(`(?i)^(?:###\s*)?Section\s+(\d+)$`)

func fixSection10(reply string) string {
	lines := strings.Split(reply, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		m := sectionNumRE.FindStringSubmatch(line)
		if m != nil {
			num := m[1]
			out = append(out, "### Section "+num)
			i++
			var desc []string
			for i < len(lines) {
				nxt := strings.TrimSpace(lines[i])
				if sectionNumRE.MatchString(nxt) {
					break
				}
				if nxt != "" {
					desc = append(desc, nxt)
				}
				i++
			}
			if len(desc) >= 2 {
				out = append(out, desc[:2]...)
			} else if len(desc) == 1 {
				first, second := splitToTwoLines(desc[0])
				out = append(out, first, second)
			} else {
				out = append(out, "", "")
			}
			continue
		}
		i++
	}
	return strings.TrimRight(strings.Join(out, "\n"), " \t\n\r")
}

var numberedRE = regexp.MustCompile(`^\d+[\.\)]`)
var separatorRowRE = regexp.MustCompile(`^[-:\s|]+$`)

func fixReleaseNotes(reply string) string {
	rawLines := strings.Split(reply, "\n")
	for i := range rawLines {
		rawLines[i] = strings.TrimRight(rawLines[i], " \t\r\n")
	}
	var strippedLines []string
	for _, ln := range rawLines {
		if strings.TrimSpace(ln) != "" {
			strippedLines = append(strippedLines, strings.TrimSpace(ln))
		}
	}

	var summaryLine string
	for _, ln := range strippedLines {
		if strings.HasPrefix(strings.ToLower(ln), "summary:") {
			summaryLine = ln
			break
		}
	}
	if summaryLine == "" {
		for _, ln := range strippedLines {
			if strings.ToLower(ln) != "release notes" {
				summaryLine = "Summary: " + ln
				break
			}
		}
	}
	if summaryLine == "" {
		summaryLine = "Summary:"
	}

	// Enforce <= 20 words after Summary:
	if strings.HasPrefix(strings.ToLower(summaryLine), "summary:") {
		parts := strings.SplitN(summaryLine, ":", 2)
		if len(parts) == 2 {
			restWords := strings.Fields(strings.TrimSpace(parts[1]))
			if len(restWords) > 20 {
				restWords = restWords[:20]
			}
			summaryLine = strings.TrimRight(parts[0]+": "+strings.Join(restWords, " "), " ")
		}
	} else {
		words := strings.Fields(summaryLine)
		if len(words) > 21 {
			summaryLine = strings.Join(words[:21], " ")
		}
	}

	var numbered []string
	for _, ln := range strippedLines {
		if numberedRE.MatchString(ln) {
			numbered = append(numbered, ln)
		}
	}
	if len(numbered) > 4 {
		numbered = numbered[:4]
	}

	var tableLines []string
	for _, ln := range rawLines {
		if strings.HasPrefix(strings.TrimSpace(ln), "|") && strings.Contains(ln, "|") {
			tableLines = append(tableLines, ln)
		}
	}

	type row struct{ item, risk, action string }
	var rows []row

	parseTableRows := func(lines []string) []row {
		var parsed []row
		for _, ln := range lines {
			if !strings.HasPrefix(strings.TrimSpace(ln), "|") {
				continue
			}
			stripped := strings.TrimSpace(ln)
			stripped = strings.Trim(stripped, "|")
			// Check for separator row
			if separatorRowRE.MatchString(stripped) {
				continue
			}
			cells := strings.Split(stripped, "|")
			if len(cells) < 3 {
				continue
			}
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			if strings.ToLower(cells[0]) == "item" && strings.ToLower(cells[1]) == "risk" {
				continue
			}
			parsed = append(parsed, row{cells[0], cells[1], cells[2]})
		}
		return parsed
	}

	if len(tableLines) > 0 {
		rows = parseTableRows(tableLines)
	} else {
		var item, risk, action string
		for _, ln := range strippedLines {
			low := strings.ToLower(ln)
			if strings.HasPrefix(low, "item:") {
				item = strings.TrimSpace(strings.SplitN(ln, ":", 2)[1])
			} else if strings.HasPrefix(low, "risk:") {
				risk = strings.TrimSpace(strings.SplitN(ln, ":", 2)[1])
			} else if strings.HasPrefix(low, "action:") {
				action = strings.TrimSpace(strings.SplitN(ln, ":", 2)[1])
				if item != "" || risk != "" || action != "" {
					rows = append(rows, row{item, risk, action})
				}
				item, risk, action = "", "", ""
			}
		}
		if len(rows) > 0 {
			tableLines = []string{"| Item | Risk | Action |", "| --- | --- | --- |"}
			for _, r := range rows {
				tableLines = append(tableLines, strings.TrimRight(fmt.Sprintf("| %s | %s | %s |", r.item, r.risk, r.action), " "))
			}
		}
	}

	if len(numbered) == 0 {
		var candidates []string
		for _, ln := range strippedLines {
			low := strings.ToLower(ln)
			if low == "release notes" {
				continue
			}
			if strings.HasPrefix(low, "summary:") || strings.HasPrefix(low, "item:") ||
				strings.HasPrefix(low, "risk:") || strings.HasPrefix(low, "action:") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(ln), "|") {
				continue
			}
			if numberedRE.MatchString(ln) {
				continue
			}
			candidates = append(candidates, ln)
		}
		if len(candidates) > 0 {
			if len(candidates) > 4 {
				candidates = candidates[:4]
			}
			for i, text := range candidates {
				numbered = append(numbered, fmt.Sprintf("%d. %s", i+1, text))
			}
		} else if len(rows) > 0 {
			for i, r := range rows {
				if i >= 4 {
					break
				}
				val := r.item
				if val == "" {
					val = r.risk
				}
				if val == "" {
					val = r.action
				}
				val = strings.TrimSpace(val)
				if val != "" {
					numbered = append(numbered, fmt.Sprintf("%d. %s", i+1, val))
				}
			}
		}
		if len(numbered) > 0 && len(numbered) < 4 {
			lastText := numbered[len(numbered)-1]
			parts := strings.SplitN(lastText, ".", 2)
			if len(parts) == 2 {
				lastText = strings.TrimSpace(parts[1])
			}
			for len(numbered) < 4 {
				numbered = append(numbered, fmt.Sprintf("%d. %s", len(numbered)+1, lastText))
			}
		}
	}

	out := []string{"### Release Notes", summaryLine}
	if len(numbered) > 0 {
		out = append(out, numbered...)
	}
	if len(tableLines) > 0 {
		out = append(out, tableLines...)
	}
	return strings.TrimRight(strings.Join(out, "\n"), " \t\n\r")
}

func claudePostprocessReply(req ProviderRequest, reply string) string {
	fixed := reply
	if shouldFixBoxTable(req.Message, fixed) {
		fixed = convertBoxTableToMarkdown(fixed)
	}
	if wantsTripletFences(req.Message) {
		fixed = fixTripletFences(fixed)
	}
	if wantsBashFence(req.Message) {
		fixed = fixBashFence(fixed)
	}
	if wantsTextFence(req.Message) {
		fixed = fixTextFence(fixed)
	}
	if wantsReleaseNotes(req.Message) || looksLikeReleaseNotesReply(fixed) {
		fixed = fixReleaseNotes(fixed)
	}
	if wantsABCSections(req.Message) {
		fixed = fixABCSections(fixed)
	}
	if wantsSection10(req.Message) {
		fixed = fixSection10(fixed)
	}
	return fixed
}
