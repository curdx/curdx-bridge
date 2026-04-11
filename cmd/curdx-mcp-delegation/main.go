package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	protocolVersion = "2024-11-05"
)

var serverInfo = map[string]string{
	"name":    "curdx-delegation",
	"version": "0.1.0",
}

var (
	cacheDir   string
	logPath    string
	cacheTTL   int64
	curdxCaller  string
	homeDir    string
	stdoutMu   sync.Mutex
)

// providers maps provider name -> {ask, pend, ping} command names.
var providers = map[string]map[string]string{
	"codex":    {"ask": "cask", "pend": "cpend", "ping": "cping"},
	"gemini":   {"ask": "gask", "pend": "gpend", "ping": "gping"},
	"claude":   {"ask": "lask", "pend": "lpend", "ping": "lping"},
	"opencode": {"ask": "oask", "pend": "opend", "ping": "oping"},
}

// providerOrder preserves iteration order matching Python.
var providerOrder = []string{"codex", "gemini", "claude", "opencode"}

type aliasEntry struct {
	name     string
	provider string
	kind     string
}

var aliasTools = []aliasEntry{
	{"cask", "codex", "ask"},
	{"gask", "gemini", "ask"},
	{"lask", "claude", "ask"},
	{"oask", "opencode", "ask"},
	{"cpend", "codex", "pend"},
	{"gpend", "gemini", "pend"},
	{"lpend", "claude", "pend"},
	{"opend", "opencode", "pend"},
	{"cping", "codex", "ping"},
	{"gping", "gemini", "ping"},
	{"lping", "claude", "ping"},
	{"oping", "opencode", "ping"},
}

var aliasMap = func() map[string][2]string {
	m := make(map[string][2]string, len(aliasTools))
	for _, a := range aliasTools {
		m[a.name] = [2]string{a.provider, a.kind}
	}
	return m
}()

// ---------- schemas ----------

func askSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message": map[string]any{
				"type":        "string",
				"description": "Request text to send to the provider.",
			},
			"timeout_s": map[string]any{
				"type":        "number",
				"description": "Timeout in seconds for the provider request.",
				"default":     120,
			},
			"session_file": map[string]any{
				"type":        "string",
				"description": "Path to the provider session file (e.g., .codex-session).",
			},
		},
		"required": []string{"message"},
	}
}

func pendSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "Task id returned by curdx_ask_* (optional: latest).",
			},
			"session_file": map[string]any{
				"type":        "string",
				"description": "Path to the provider session file (optional fallback).",
			},
		},
		"required": []string{},
	}
}

func pingSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_file": map[string]any{
				"type":        "string",
				"description": "Path to the provider session file (optional).",
			},
		},
		"required": []string{},
	}
}

// ---------- tool definitions ----------

var toolDefs []map[string]any

func init() {
	toolDefs = buildToolDefs()
}

func buildToolDefs() []map[string]any {
	var defs []map[string]any
	for _, provider := range providerOrder {
		defs = append(defs, map[string]any{
			"name":        fmt.Sprintf("curdx_ask_%s", provider),
			"description": fmt.Sprintf("Submit a background request to %s (CURDX).", provider),
			"inputSchema": askSchema(),
		})
		defs = append(defs, map[string]any{
			"name":        fmt.Sprintf("curdx_pend_%s", provider),
			"description": fmt.Sprintf("Fetch the result of a background %s request.", provider),
			"inputSchema": pendSchema(),
		})
		defs = append(defs, map[string]any{
			"name":        fmt.Sprintf("curdx_ping_%s", provider),
			"description": fmt.Sprintf("Check availability for %s in CURDX.", provider),
			"inputSchema": pingSchema(),
		})
	}

	for _, a := range aliasTools {
		var schema map[string]any
		switch a.kind {
		case "ask":
			schema = askSchema()
		case "pend":
			schema = pendSchema()
		default:
			schema = pingSchema()
		}
		defs = append(defs, map[string]any{
			"name":        a.name,
			"description": fmt.Sprintf("Alias for curdx_%s_%s.", a.kind, a.provider),
			"inputSchema": schema,
		})
	}
	return defs
}

// ---------- logging ----------

func logMsg(message string) {
	func() {
		defer func() { recover() }()
		ensureCache()
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
		defer f.Close()
		ts := time.Now().Format("2006-01-02 15:04:05")
		fmt.Fprintf(f, "[%s] %s\n", ts, message)
	}()
}

// ---------- cache ----------

func ensureCache() {
	os.MkdirAll(cacheDir, 0755)
}

func cleanupCache() {
	if cacheTTL <= 0 {
		return
	}
	now := time.Now()
	func() {
		defer func() { recover() }()
		ensureCache()
		entries, err := os.ReadDir(cacheDir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			p := filepath.Join(cacheDir, e.Name())
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			if now.Sub(info.ModTime()).Seconds() > float64(cacheTTL) {
				base := strings.TrimSuffix(e.Name(), ".json")
				outPath := filepath.Join(cacheDir, base+".out")
				os.Remove(p)
				os.Remove(outPath)
			}
		}
	}()
}

// ---------- JSON I/O ----------

func send(obj map[string]any) {
	data, err := json.Marshal(obj)
	if err != nil {
		return
	}
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	os.Stdout.Write(data)
	os.Stdout.Write([]byte("\n"))
}

func rpcResult(reqID any, result any) {
	send(map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"result":  result,
	})
}

func rpcError(reqID any, code int, message string) {
	send(map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func toolOK(payload map[string]any) map[string]any {
	text, _ := json.Marshal(payload)
	return map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": string(text),
			},
		},
	}
}

func toolError(message string) map[string]any {
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": message},
		},
		"isError": true,
	}
}

// ---------- session file validation ----------

func validateSessionFile(sessionFile string) string {
	if sessionFile == "" {
		return ""
	}
	resolved := sessionFile
	if strings.HasPrefix(resolved, "~") {
		resolved = filepath.Join(homeDir, resolved[1:])
	}
	resolved, err := filepath.Abs(resolved)
	if err != nil {
		return fmt.Sprintf("session_file path not allowed: %s", sessionFile)
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		// If the file doesn't exist yet, try resolving parent.
		resolved2, err2 := filepath.Abs(sessionFile)
		if err2 != nil {
			return fmt.Sprintf("session_file path not allowed: %s", sessionFile)
		}
		resolved = resolved2
	}

	homeResolved, _ := filepath.EvalSymlinks(homeDir)
	if homeResolved == "" {
		homeResolved = homeDir
	}
	tmpResolved, _ := filepath.EvalSymlinks("/tmp")
	if tmpResolved == "" {
		tmpResolved = "/tmp"
	}

	allowed := []string{homeResolved, tmpResolved}
	for _, dir := range allowed {
		rel, err := filepath.Rel(dir, resolved)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return ""
		}
	}
	return fmt.Sprintf("session_file path not allowed: %s", sessionFile)
}

// ---------- task ID / paths ----------

func makeTaskID(provider string) string {
	ts := time.Now().Unix()
	b := make([]byte, 2)
	rand.Read(b)
	return fmt.Sprintf("%s-%d-%s", provider, ts, hex.EncodeToString(b))
}

func metaPath(taskID string) string {
	return filepath.Join(cacheDir, taskID+".json")
}

func outputPath(taskID string) string {
	return filepath.Join(cacheDir, taskID+".out")
}

// ---------- JSON file helpers ----------

func readJSON(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

func writeJSON(path string, data map[string]any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	os.WriteFile(path, b, 0644)
}

// ---------- background spawn ----------

func spawnBackground(cmd []string, message string, metaFilePath string) *int {
	ensureCache()

	env := os.Environ()
	// Set CURDX_CALLER in env
	found := false
	for i, e := range env {
		if strings.HasPrefix(e, "CURDX_CALLER=") {
			env[i] = "CURDX_CALLER=" + curdxCaller
			found = true
			break
		}
	}
	if !found {
		env = append(env, "CURDX_CALLER="+curdxCaller)
	}

	stderrFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logMsg(fmt.Sprintf("spawn failed cmd=%v err=%v", cmd, err))
		return nil
	}

	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin = strings.NewReader(message)
	c.Stdout = nil // /dev/null
	c.Stderr = stderrFile
	c.Env = env
	setSysProcAttr(c)

	if err := c.Start(); err != nil {
		stderrFile.Close()
		logMsg(fmt.Sprintf("spawn failed cmd=%v err=%v", cmd, err))
		return nil
	}
	stderrFile.Close()

	pid := c.Process.Pid

	go func() {
		var exitCode *int
		err := c.Wait()
		if err == nil {
			zero := 0
			exitCode = &zero
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			exitCode = &code
		}

		meta := readJSON(metaFilePath)
		if meta == nil {
			meta = make(map[string]any)
		}
		if exitCode != nil {
			meta["exit_code"] = *exitCode
		} else {
			meta["exit_code"] = nil
		}
		meta["finished_at"] = time.Now().Unix()
		if exitCode != nil && *exitCode == 0 {
			meta["status"] = "completed"
		} else {
			meta["status"] = "error"
		}
		writeJSON(metaFilePath, meta)
	}()

	return &pid
}

// ---------- resolve provider ----------

func resolveProvider(name string) string {
	for _, p := range providerOrder {
		if strings.HasSuffix(name, p) {
			return p
		}
	}
	return ""
}

// ---------- tool handlers ----------

func submitTask(provider string, args map[string]any) map[string]any {
	message := strings.TrimSpace(toString(args["message"]))
	if message == "" {
		return toolError("message is required")
	}

	timeoutS := 120.0
	if v, ok := args["timeout_s"]; ok && v != nil {
		switch t := v.(type) {
		case float64:
			timeoutS = t
		case string:
			if f, err := strconv.ParseFloat(t, 64); err == nil {
				timeoutS = f
			}
		}
	}

	sessionFile := strings.TrimSpace(toString(args["session_file"]))
	if errMsg := validateSessionFile(sessionFile); errMsg != "" {
		return toolError(errMsg)
	}
	if sessionFile != "" {
		expanded := sessionFile
		if strings.HasPrefix(expanded, "~") {
			expanded = filepath.Join(homeDir, expanded[1:])
		}
		if _, err := os.Stat(expanded); os.IsNotExist(err) {
			return toolError(fmt.Sprintf("session_file not found: %s", sessionFile))
		}
	}

	taskID := makeTaskID(provider)
	outPath := outputPath(taskID)
	metaFilePath := metaPath(taskID)

	cmdArgs := []string{
		providers[provider]["ask"],
		"--sync", "--output", outPath,
		"--timeout", fmt.Sprintf("%g", timeoutS),
		"-q",
	}
	if sessionFile != "" {
		cmdArgs = append(cmdArgs, "--session-file", sessionFile)
	}

	meta := map[string]any{
		"task_id":     taskID,
		"provider":    provider,
		"output_file": outPath,
		"session_file": func() any {
			if sessionFile == "" {
				return nil
			}
			return sessionFile
		}(),
		"status":     "running",
		"started_at": time.Now().Unix(),
		"exit_code":  nil,
	}
	writeJSON(metaFilePath, meta)

	pid := spawnBackground(cmdArgs, message, metaFilePath)
	if pid == nil {
		return toolError("failed to launch provider command")
	}

	meta["pid"] = *pid
	writeJSON(metaFilePath, meta)

	return toolOK(map[string]any{
		"task_id":     taskID,
		"status":      "submitted",
		"output_file": outPath,
	})
}

func loadLatestMeta(provider string) map[string]any {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return nil
	}

	type metaEntry struct {
		mtime time.Time
		data  map[string]any
	}
	var metas []metaEntry

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(cacheDir, e.Name())
		data := readJSON(p)
		if data == nil {
			continue
		}
		if toString(data["provider"]) != provider {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		metas = append(metas, metaEntry{mtime: info.ModTime(), data: data})
	}

	if len(metas) == 0 {
		return nil
	}

	// Find latest by mtime
	best := metas[0]
	for _, m := range metas[1:] {
		if m.mtime.After(best.mtime) {
			best = m
		}
	}
	return best.data
}

func pendTask(provider string, args map[string]any) map[string]any {
	taskID := strings.TrimSpace(toString(args["task_id"]))
	sessionFile := strings.TrimSpace(toString(args["session_file"]))
	if errMsg := validateSessionFile(sessionFile); errMsg != "" {
		return toolError(errMsg)
	}

	var meta map[string]any
	if taskID != "" {
		meta = readJSON(metaPath(taskID))
		if meta == nil {
			return toolError(fmt.Sprintf("unknown task_id: %s", taskID))
		}
	} else {
		meta = loadLatestMeta(provider)
		if meta == nil {
			return pendFallback(provider, sessionFile)
		}
	}

	outPathStr := toString(meta["output_file"])
	reply := ""
	status := toString(meta["status"])
	if status == "" {
		status = "pending"
	}
	if outPathStr != "" {
		data, err := os.ReadFile(outPathStr)
		if err == nil {
			reply = strings.TrimSpace(string(data))
			if reply != "" {
				status = "completed"
			}
		}
	}

	return toolOK(map[string]any{
		"task_id":     orNil(toString(meta["task_id"]), taskID),
		"status":      status,
		"reply":       reply,
		"output_file": meta["output_file"],
		"exit_code":   meta["exit_code"],
	})
}

func pendFallback(provider string, sessionFile string) map[string]any {
	cmdArgs := []string{providers[provider]["pend"]}
	if sessionFile != "" {
		cmdArgs = append(cmdArgs, "--session-file", sessionFile)
	}
	c := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	var stdoutBuf, stderrBuf strings.Builder
	c.Stdout = &stdoutBuf
	c.Stderr = &stderrBuf
	err := c.Run()
	if err != nil {
		msg := strings.TrimSpace(stdoutBuf.String())
		if msg == "" {
			msg = strings.TrimSpace(stderrBuf.String())
		}
		if msg == "" {
			msg = "pend failed"
		}
		return toolError(msg)
	}
	return toolOK(map[string]any{
		"status": "completed",
		"reply":  strings.TrimSpace(stdoutBuf.String()),
	})
}

func pingProvider(provider string, args map[string]any) map[string]any {
	sessionFile := strings.TrimSpace(toString(args["session_file"]))
	if errMsg := validateSessionFile(sessionFile); errMsg != "" {
		return toolError(errMsg)
	}
	cmdArgs := []string{providers[provider]["ping"]}
	if sessionFile != "" {
		cmdArgs = append(cmdArgs, "--session-file", sessionFile)
	}
	c := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	var stdoutBuf, stderrBuf strings.Builder
	c.Stdout = &stdoutBuf
	c.Stderr = &stderrBuf
	err := c.Run()
	available := err == nil
	msg := strings.TrimSpace(stdoutBuf.String())
	if msg == "" {
		msg = strings.TrimSpace(stderrBuf.String())
	}
	return toolOK(map[string]any{
		"available": available,
		"message":   msg,
	})
}

// ---------- dispatch ----------

func handleToolCall(name string, args map[string]any) map[string]any {
	if alias, ok := aliasMap[name]; ok {
		provider, kind := alias[0], alias[1]
		switch kind {
		case "ask":
			return submitTask(provider, args)
		case "pend":
			return pendTask(provider, args)
		case "ping":
			return pingProvider(provider, args)
		default:
			return toolError(fmt.Sprintf("unknown tool kind: %s", kind))
		}
	}

	provider := resolveProvider(name)
	if provider == "" {
		return toolError(fmt.Sprintf("unknown tool: %s", name))
	}
	if _, ok := providers[provider]; !ok {
		return toolError(fmt.Sprintf("unknown tool: %s", name))
	}

	if strings.HasPrefix(name, "curdx_ask_") {
		return submitTask(provider, args)
	}
	if strings.HasPrefix(name, "curdx_pend_") {
		return pendTask(provider, args)
	}
	if strings.HasPrefix(name, "curdx_ping_") {
		return pingProvider(provider, args)
	}
	return toolError(fmt.Sprintf("unknown tool: %s", name))
}

func handleRequest(msg map[string]any) bool {
	method := toString(msg["method"])
	reqID := msg["id"]

	switch method {
	case "initialize":
		params, _ := msg["params"].(map[string]any)
		proto := protocolVersion
		if params != nil {
			if pv := toString(params["protocolVersion"]); pv != "" {
				proto = pv
			}
		}
		result := map[string]any{
			"protocolVersion": proto,
			"capabilities":   map[string]any{"tools": map[string]any{"list": true}},
			"serverInfo":     serverInfo,
		}
		rpcResult(reqID, result)
		return false

	case "initialized":
		return false

	case "tools/list":
		rpcResult(reqID, map[string]any{"tools": toolDefs})
		return false

	case "tools/call":
		params, _ := msg["params"].(map[string]any)
		if params == nil {
			params = make(map[string]any)
		}
		name := toString(params["name"])
		args, _ := params["arguments"].(map[string]any)
		if args == nil {
			args = make(map[string]any)
		}
		if name == "" {
			rpcError(reqID, -32602, "missing tool name")
			return false
		}
		result := handleToolCall(name, args)
		rpcResult(reqID, result)
		return false

	case "shutdown", "exit":
		rpcResult(reqID, map[string]any{})
		return true

	default:
		if reqID != nil {
			rpcError(reqID, -32601, fmt.Sprintf("unknown method: %s", method))
		}
		return false
	}
}

// ---------- helpers ----------

func toString(v any) string {
	if v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	default:
		return fmt.Sprintf("%v", v)
	}
}

func orNil(primary, fallback string) any {
	if primary != "" {
		return primary
	}
	if fallback != "" {
		return fallback
	}
	return nil
}

// ---------- main ----------

func main() {
	// Initialize globals
	homeDir, _ = os.UserHomeDir()

	if v := os.Getenv("CURDX_DELEGATION_CACHE_DIR"); v != "" {
		cacheDir = v
	} else {
		cacheDir = filepath.Join(homeDir, ".cache", "curdx", "delegation")
	}
	logPath = filepath.Join(cacheDir, "mcp-server.log")

	if v := os.Getenv("CURDX_DELEGATION_TTL_S"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cacheTTL = n
		} else {
			cacheTTL = 86400
		}
	} else {
		cacheTTL = 86400
	}

	curdxCaller = os.Getenv("CURDX_CALLER")
	if curdxCaller == "" {
		curdxCaller = "claude"
	}

	cleanupCache()

	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer for large JSON-RPC messages
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			continue
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					logMsg(fmt.Sprintf("handle_request panic: %v", r))
					reqID := msg["id"]
					if reqID != nil {
						rpcError(reqID, -32603, "internal error")
					}
				}
			}()
			if handleRequest(msg) {
				os.Exit(0)
			}
		}()
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		logMsg(fmt.Sprintf("stdin read error: %v", err))
	}
}
