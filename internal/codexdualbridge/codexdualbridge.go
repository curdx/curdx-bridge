// Package codexdualbridge provides the Claude-Codex dual-window bridge.
// Source: claude_code_bridge/lib/codex_dual_bridge.py
package codexdualbridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/anthropics/curdx-bridge/internal/terminal"
)

func envFloat(name string, defaultVal float64) float64 {
	raw := os.Getenv(name)
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return defaultVal
	}
	if v < 0 {
		return 0
	}
	return v
}

// TerminalCodexSession injects commands to Codex CLI via terminal session.
type TerminalCodexSession struct {
	TerminalType string
	PaneID       string
	Backend      terminal.TerminalBackend
}

// Send sends text to the terminal pane.
func (s *TerminalCodexSession) Send(text string) {
	command := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r", " "), "\n", " "))
	if command == "" {
		return
	}
	if s.Backend == nil || s.PaneID == "" {
		return
	}
	_ = s.Backend.SendText(s.PaneID, command)
}

// DualBridge is the Claude-Codex bridge main process.
type DualBridge struct {
	RuntimeDir  string
	SessionID   string
	InputFIFO   string
	HistoryDir  string
	HistoryFile string
	BridgeLog   string
	CodexSession *TerminalCodexSession
	running     atomic.Bool
}

// NewDualBridge creates a new DualBridge.
func NewDualBridge(runtimeDir, sessionID string) (*DualBridge, error) {
	historyDir := filepath.Join(runtimeDir, "history")
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return nil, fmt.Errorf("create history dir: %w", err)
	}

	terminalType := os.Getenv("CODEX_TERMINAL")
	if terminalType == "" {
		terminalType = "tmux"
	}

	var paneID string
	if terminalType == "wezterm" {
		paneID = os.Getenv("CODEX_WEZTERM_PANE")
	} else {
		paneID = os.Getenv("CODEX_TMUX_SESSION")
	}
	if paneID == "" {
		envName := "CODEX_TMUX_SESSION"
		if terminalType == "wezterm" {
			envName = "CODEX_WEZTERM_PANE"
		}
		return nil, fmt.Errorf("missing %s environment variable", envName)
	}

	db := &DualBridge{
		RuntimeDir:  runtimeDir,
		SessionID:   sessionID,
		InputFIFO:   filepath.Join(runtimeDir, "input.fifo"),
		HistoryDir:  historyDir,
		HistoryFile: filepath.Join(historyDir, "session.jsonl"),
		BridgeLog:   filepath.Join(runtimeDir, "bridge.log"),
		CodexSession: &TerminalCodexSession{
			TerminalType: terminalType,
			PaneID:       paneID,
			Backend:      terminal.GetBackend(terminalType),
		},
	}
	db.running.Store(true)
	return db, nil
}

// Run starts the bridge main loop.
func (b *DualBridge) Run() int {
	b.logConsole("Codex bridge started, waiting for Claude commands...")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		b.running.Store(false)
		b.logConsole(fmt.Sprintf("Received signal %v, exiting...", sig))
	}()

	idleSleep := envFloat("CCB_BRIDGE_IDLE_SLEEP", 0.05)
	errorBackoffMin := envFloat("CCB_BRIDGE_ERROR_BACKOFF_MIN", 0.05)
	errorBackoffMax := envFloat("CCB_BRIDGE_ERROR_BACKOFF_MAX", 0.2)
	errorBackoff := errorBackoffMin
	if errorBackoff > errorBackoffMax {
		errorBackoff = errorBackoffMax
	}

	for b.running.Load() {
		payloads, err := b.readRequests()
		if err != nil {
			b.logConsole(fmt.Sprintf("Failed to process message: %v", err))
			b.logBridge(fmt.Sprintf("error: %v", err))
			if errorBackoff > 0 {
				time.Sleep(time.Duration(errorBackoff * float64(time.Second)))
			}
			errorBackoff = min(errorBackoffMax, max(errorBackoffMin, errorBackoff*2))
			continue
		}
		if len(payloads) == 0 {
			if idleSleep > 0 {
				time.Sleep(time.Duration(idleSleep * float64(time.Second)))
			}
			continue
		}
		for _, payload := range payloads {
			b.processRequest(payload)
		}
		errorBackoff = errorBackoffMin
		if errorBackoff > errorBackoffMax {
			errorBackoff = errorBackoffMax
		}
	}

	b.logConsole("Codex bridge exited")
	return 0
}

func (b *DualBridge) readRequests() ([]map[string]any, error) {
	if _, err := os.Stat(b.InputFIFO); err != nil {
		return nil, nil
	}
	f, err := os.Open(b.InputFIFO)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var payloads []map[string]any
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			var payload map[string]any
			if jsonErr := json.Unmarshal([]byte(line), &payload); jsonErr == nil {
				payloads = append(payloads, payload)
			}
		}
		if err != nil {
			break
		}
	}
	return payloads, nil
}

func (b *DualBridge) processRequest(payload map[string]any) {
	content, _ := payload["content"].(string)
	marker, _ := payload["marker"].(string)
	if marker == "" {
		marker = generateMarker()
	}

	ts := timestamp()
	entry, _ := json.Marshal(map[string]any{
		"marker": marker, "question": content, "time": ts,
	})
	b.logBridge(string(entry))
	b.appendHistory("claude", content, marker)

	b.CodexSession.Send(content)
}

func (b *DualBridge) appendHistory(role, content, marker string) {
	entry := map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"role":      role,
		"marker":    marker,
		"content":   content,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(b.HistoryFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(data)
	f.WriteString("\n")
}

func (b *DualBridge) logBridge(message string) {
	f, err := os.OpenFile(b.BridgeLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", timestamp(), message)
}

func (b *DualBridge) logConsole(message string) {
	fmt.Println(message)
}

func timestamp() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func generateMarker() string {
	return fmt.Sprintf("ask-%d-%d", time.Now().Unix(), os.Getpid())
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
