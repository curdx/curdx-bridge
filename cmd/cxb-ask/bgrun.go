package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/curdx/curdx-bridge/internal/cliutil"
)

// bgRunConfig carries everything the background wrapper needs to run a task.
// It is serialized as JSON and handed to a detached `cxb-ask --bg-run` child
// so the submission path works identically on Unix and Windows without
// relying on `/bin/sh` or PowerShell.
type bgRunConfig struct {
	TaskID      string            `json:"task_id"`
	Provider    string            `json:"provider"`
	Caller      string            `json:"caller"`
	WorkDir     string            `json:"work_dir"`
	AskCmd      string            `json:"ask_cmd"`
	Timeout     float64           `json:"timeout"`
	StatusFile  string            `json:"status_file"`
	LogFile     string            `json:"log_file"`
	MessageFile string            `json:"message_file"`
	Env         map[string]string `json:"env,omitempty"`
}

// runBackgroundTask is the entry point for `cxb-ask --bg-run <config.json>`.
// It replaces the previous `#!/bin/sh` wrapper and therefore works on any
// platform Go supports (Unix, macOS, Windows native, and WSL).
func runBackgroundTask(configPath string) int {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] bg-run: failed to read config %s: %v\n", configPath, err)
		return cliutil.ExitError
	}
	var cfg bgRunConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] bg-run: invalid config %s: %v\n", configPath, err)
		return cliutil.ExitError
	}
	defer os.Remove(configPath)
	defer func() {
		if cfg.MessageFile != "" {
			os.Remove(cfg.MessageFile)
		}
	}()

	appendTaskStatusLine(cfg.StatusFile, fmt.Sprintf("running pid=%d", os.Getpid()))

	logHandle, err := os.OpenFile(cfg.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] bg-run: open log %s: %v\n", cfg.LogFile, err)
		return cliutil.ExitError
	}
	defer logHandle.Close()

	fmt.Fprintf(logHandle, "[CURDX_TASK_START] task=%s provider=%s caller=%s pid=%d\n",
		cfg.TaskID, cfg.Provider, cfg.Caller, os.Getpid())

	msgFile, err := os.Open(cfg.MessageFile)
	if err != nil {
		fmt.Fprintf(logHandle, "[CURDX_TASK_END] task=%s provider=%s exit_code=1\n",
			cfg.TaskID, cfg.Provider)
		appendTaskStatusLine(cfg.StatusFile, fmt.Sprintf("failed exit_code=1 (message read: %v)", err))
		return cliutil.ExitError
	}
	defer msgFile.Close()

	child := exec.Command(cfg.AskCmd,
		cfg.Provider,
		"--foreground",
		"--timeout", strconv.FormatFloat(cfg.Timeout, 'f', -1, 64),
	)
	child.Stdin = msgFile
	child.Stdout = logHandle
	child.Stderr = logHandle
	child.Env = mergeEnv(os.Environ(), cfg.Env)

	rc := 0
	if err := child.Run(); err != nil {
		if child.ProcessState != nil {
			rc = child.ProcessState.ExitCode()
		} else {
			rc = 1
		}
	}

	fmt.Fprintf(logHandle, "[CURDX_TASK_END] task=%s provider=%s exit_code=%d\n",
		cfg.TaskID, cfg.Provider, rc)

	appendTaskStatusLine(cfg.StatusFile, fmt.Sprintf("finished exit_code=%d", rc))
	if rc != 0 {
		appendTaskStatusLine(cfg.StatusFile, fmt.Sprintf("failed exit_code=%d", rc))
	}

	return rc
}

// mergeEnv returns base with extra overriding any matching keys.
func mergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	keep := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			keep = append(keep, kv)
			continue
		}
		if _, override := extra[kv[:eq]]; !override {
			keep = append(keep, kv)
		}
	}
	for k, v := range extra {
		keep = append(keep, k+"="+v)
	}
	return keep
}

// spawnBackgroundTask writes message+config to temp files and launches
// `self --bg-run <config>` as a detached child. Returns the child pid.
func spawnBackgroundTask(cfg bgRunConfig, selfPath, messageBody string) (int, error) {
	dir := filepath.Dir(cfg.LogFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, fmt.Errorf("mkdir %s: %w", dir, err)
	}

	msgPath := filepath.Join(dir, fmt.Sprintf("cxb-ask-%s-%s.msg", cfg.Provider, cfg.TaskID))
	if err := os.WriteFile(msgPath, []byte(messageBody), 0o600); err != nil {
		return 0, fmt.Errorf("write message file: %w", err)
	}
	cfg.MessageFile = msgPath

	data, err := json.Marshal(cfg)
	if err != nil {
		os.Remove(msgPath)
		return 0, fmt.Errorf("marshal config: %w", err)
	}
	configPath := filepath.Join(dir, fmt.Sprintf("cxb-ask-%s-%s.cfg.json", cfg.Provider, cfg.TaskID))
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		os.Remove(msgPath)
		return 0, fmt.Errorf("write config file: %w", err)
	}

	logHandle, err := os.OpenFile(cfg.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Remove(msgPath)
		os.Remove(configPath)
		return 0, fmt.Errorf("open log: %w", err)
	}
	defer logHandle.Close()

	proc := exec.Command(selfPath, "--bg-run", configPath)
	proc.Stdin = nil
	proc.Stdout = logHandle
	proc.Stderr = logHandle
	setSysProcAttr(proc)

	if err := proc.Start(); err != nil {
		os.Remove(msgPath)
		os.Remove(configPath)
		return 0, err
	}
	pid := proc.Process.Pid
	go func() { _ = proc.Wait() }()
	return pid, nil
}