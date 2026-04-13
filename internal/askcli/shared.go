// Package askcli provides shared CLI logic for all provider ask commands.
// Source: claude_code_bridge/bin/cask (template), bin/gask, bin/oask, etc.
//
// Each provider CLI (cask, gask, oask, lask) follows the
// same pattern:
//  1. Parse CLI args (message from args or stdin)
//  2. Resolve work_dir
//  3. Try daemon request via RPC
//  4. Fall back to error if daemon unavailable
//  5. Print reply or write to --output file
//
// The per-provider differences are encoded in ProviderCLIConfig.
package askcli

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/curdx/curdx-bridge/internal/cliutil"
	"github.com/curdx/curdx-bridge/internal/compat"
	"github.com/curdx/curdx-bridge/internal/envutil"
	"github.com/curdx/curdx-bridge/internal/paneregistry"
	"github.com/curdx/curdx-bridge/internal/projectid"
	"github.com/curdx/curdx-bridge/internal/protocol"
	"github.com/curdx/curdx-bridge/internal/providers"
	"github.com/curdx/curdx-bridge/internal/rpc"
	"github.com/curdx/curdx-bridge/internal/runtime"
	"github.com/curdx/curdx-bridge/internal/sessionutil"
)

// ProviderCLIConfig holds per-provider configuration for the shared CLI logic.
type ProviderCLIConfig struct {
	// CmdName is the CLI command name (e.g. "cxb-codex-ask", "cxb-gemini-ask").
	CmdName string
	// ProviderName is the human-readable provider name (e.g. "Codex", "Gemini").
	ProviderName string
	// ProviderKey is the lowercase provider key (e.g. "codex", "gemini").
	ProviderKey string
	// Spec is the provider client spec.
	Spec providers.ProviderClientSpec
	// AsyncGuardrail is the guardrail message printed to stderr.
	AsyncGuardrail string
	// DefaultTimeout is the default timeout from CURDX_SYNC_TIMEOUT.
	// Use -1.0 for cask/lask style, 3600.0 for others.
	DefaultTimeout float64
	// HasRetryLoop controls whether the CLI retries daemon connections
	// (gask, oask pattern) or uses simpler logic (cask, lask).
	HasRetryLoop bool
	// StartupWaitEnv is the env var for startup wait override (e.g. "CURDX_GASKD_STARTUP_WAIT_S").
	StartupWaitEnv string
	// RetryWaitEnv is the env var for retry wait override (e.g. "CURDX_GASKD_RETRY_WAIT_S").
	RetryWaitEnv string
	// DaemonHint is the hint for how to start the daemon (e.g. "caskd", "gaskd", "askd").
	DaemonHint string
	// DaemonAutostartEnvHint (e.g. "CURDX_CASKD_AUTOSTART=1").
	DaemonAutostartEnvHint string
	// SetupHint is the setup command hint (e.g. "`curdx codex`").
	SetupHint string
	// HasSupervisorMode enables the codex+opencode supervisor prompt (cask only).
	HasSupervisorMode bool
	// HasAsyncMode enables --async flag (oask, lask).
	HasAsyncMode bool
}

// parsedArgs holds the result of CLI argument parsing.
type parsedArgs struct {
	outputPath  string
	timeout     float64
	message     string
	quiet       bool
	sessionFile string
	syncMode    bool
	asyncMode   bool
}

// Run is the main entry point for all provider CLIs. Call from cmd/*/main.go.
func Run(cfg ProviderCLIConfig) int {
	code := run(cfg, os.Args)
	return code
}

func run(cfg ProviderCLIConfig, argv []string) int {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", r)
		}
	}()

	args, err := parseArgs(cfg, argv)
	if err != nil {
		if err.Error() == "help" {
			return cliutil.ExitOK
		}
		fmt.Fprintf(os.Stderr, "[ERROR] %s\n", err)
		return cliutil.ExitError
	}

	if args.message == "" && !isTerminal(os.Stdin) {
		args.message = strings.TrimSpace(compat.ReadStdinText())
	}
	if args.message == "" {
		usage(cfg)
		return cliutil.ExitError
	}

	if args.asyncMode {
		args.syncMode = false
	}

	// Supervisor mode (cask only)
	if cfg.HasSupervisorMode {
		executor := getExecutorFromRoles()
		if executor == "codex+opencode" {
			args.message = supervisorPrompt + args.message
		}
	}

	workDir, _ := resolveWorkDirWithRegistry(
		cfg.Spec,
		cfg.ProviderKey,
		args.sessionFile,
		os.Getenv("CURDX_SESSION_FILE"),
	)

	var daemonResult *daemonResponse

	if cfg.HasRetryLoop {
		daemonResult = daemonRequestWithRetries(cfg, workDir, args.message, args.timeout, args.quiet, args.outputPath)
	} else {
		// Simple daemon path (cask, lask pattern)
		stateFile := stateFileFromEnv(cfg.Spec.StateFileEnv)
		daemonResult = tryDaemonRequest(cfg.Spec, workDir, args.message, args.timeout, args.quiet, stateFile, args.outputPath)
		if daemonResult == nil && maybeStartDaemon(cfg.Spec, workDir) {
			waitTimeout := math.Min(2.0, math.Max(0.2, args.timeout))
			waitForDaemonReady(cfg.Spec, waitTimeout, stateFile)
			daemonResult = tryDaemonRequest(cfg.Spec, workDir, args.message, args.timeout, args.quiet, stateFile, args.outputPath)
		}
	}

	if daemonResult != nil {
		if !args.syncMode {
			fmt.Fprint(os.Stderr, cfg.AsyncGuardrail)
			os.Stderr.Sync()
		}
		if args.outputPath != "" {
			if err := cliutil.AtomicWriteText(args.outputPath, daemonResult.reply+"\n"); err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR] Failed to write output: %s\n", err)
				return cliutil.ExitError
			}
			return daemonResult.exitCode
		}
		if !args.asyncMode {
			os.Stdout.WriteString(daemonResult.reply)
			if !strings.HasSuffix(daemonResult.reply, "\n") {
				os.Stdout.WriteString("\n")
			}
		}
		return daemonResult.exitCode
	}

	// Daemon not available -- error fallback
	if !envutil.EnvBool(cfg.Spec.EnabledEnv, true) {
		fmt.Fprintf(os.Stderr, "[ERROR] %s=0: %s daemon mode disabled.\n", cfg.Spec.EnabledEnv, cfg.CmdName)
		return cliutil.ExitError
	}
	if sessionutil.FindProjectSessionFile(workDir, cfg.Spec.SessionFilename) == "" {
		fmt.Fprintf(os.Stderr, "[ERROR] No active %s session found for this directory.\n", cfg.ProviderName)
		fmt.Fprintf(os.Stderr, "Run %s (or add %s to curdx.config) in this project first.\n", cfg.SetupHint, cfg.ProviderKey)
		return cliutil.ExitError
	}
	fmt.Fprintf(os.Stderr, "[ERROR] %s daemon required but not available.\n", cfg.CmdName)
	fmt.Fprintf(os.Stderr, "Start it with `%s` (or enable autostart via %s).\n", cfg.DaemonHint, cfg.DaemonAutostartEnvHint)
	return cliutil.ExitError
}

// -- Argument parsing --

func parseArgs(cfg ProviderCLIConfig, argv []string) (*parsedArgs, error) {
	args := &parsedArgs{}
	var parts []string

	i := 1
	for i < len(argv) {
		token := argv[i]
		i++

		switch token {
		case "-h", "--help":
			usage(cfg)
			return nil, fmt.Errorf("help")
		case "-q", "--quiet":
			args.quiet = true
		case "--sync":
			args.syncMode = true
		case "--async":
			if cfg.HasAsyncMode {
				args.asyncMode = true
			} else {
				parts = append(parts, token)
			}
		case "--session-file":
			if i >= len(argv) {
				return nil, fmt.Errorf("--session-file requires a file path")
			}
			args.sessionFile = argv[i]
			i++
		case "-o", "--output":
			if i >= len(argv) {
				return nil, fmt.Errorf("--output requires a file path")
			}
			p := argv[i]
			i++
			if strings.HasPrefix(p, "~") {
				if home, err := os.UserHomeDir(); err == nil {
					p = filepath.Join(home, p[1:])
				}
			}
			args.outputPath = p
		case "-t", "--timeout":
			if i >= len(argv) {
				return nil, fmt.Errorf("--timeout requires a number")
			}
			val, err := strconv.ParseFloat(argv[i], 64)
			if err != nil {
				return nil, fmt.Errorf("Invalid --timeout: %s", argv[i])
			}
			args.timeout = val
			i++
		default:
			parts = append(parts, token)
		}
	}

	args.message = strings.TrimSpace(strings.Join(parts, " "))

	if args.timeout == 0 {
		envVal := os.Getenv("CURDX_SYNC_TIMEOUT")
		if envVal != "" {
			v, err := strconv.ParseFloat(envVal, 64)
			if err == nil {
				args.timeout = v
			} else {
				args.timeout = cfg.DefaultTimeout
			}
		} else {
			args.timeout = cfg.DefaultTimeout
		}
	}
	if args.asyncMode {
		args.timeout = 0.0
	}

	return args, nil
}

func usage(cfg ProviderCLIConfig) {
	flags := "--sync"
	if cfg.HasAsyncMode {
		flags = "--async] [--sync"
	}
	fmt.Fprintf(os.Stderr, "Usage: %s [%s] [--session-file FILE] [--timeout SECONDS] [--output FILE] <message>\n",
		cfg.CmdName, flags)
}

// -- Work dir resolution --

func resolveWorkDir(
	spec providers.ProviderClientSpec,
	cliSessionFile string,
	envSessionFile string,
) (string, string) {
	raw := strings.TrimSpace(cliSessionFile)
	if raw == "" {
		raw = strings.TrimSpace(envSessionFile)
	}
	if raw == "" {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}
		return cwd, ""
	}

	if strings.HasPrefix(raw, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			raw = filepath.Join(home, raw[1:])
		}
	}

	// In Claude Code, require absolute path
	if os.Getenv("CLAUDECODE") == "1" && !filepath.IsAbs(raw) {
		panic(fmt.Sprintf("--session-file must be an absolute path in Claude Code (got: %s)", raw))
	}

	sessionPath, err := filepath.Abs(raw)
	if err != nil {
		sessionPath = raw
	}
	if resolved, err := filepath.EvalSymlinks(sessionPath); err == nil {
		sessionPath = resolved
	}

	if filepath.Base(sessionPath) != spec.SessionFilename {
		panic(fmt.Sprintf("Invalid session file for %s: expected filename %s, got %s",
			spec.ProtocolPrefix, spec.SessionFilename, filepath.Base(sessionPath)))
	}
	if _, err := os.Stat(sessionPath); err != nil {
		panic(fmt.Sprintf("Session file not found: %s", sessionPath))
	}

	parent := filepath.Base(filepath.Dir(sessionPath))
	if parent == sessionutil.CURDXProjectConfigDirname || parent == sessionutil.CURDXProjectConfigLegacyDirname {
		return filepath.Dir(filepath.Dir(sessionPath)), sessionPath
	}
	return filepath.Dir(sessionPath), sessionPath
}

func resolveWorkDirWithRegistry(
	spec providers.ProviderClientSpec,
	provider string,
	cliSessionFile string,
	envSessionFile string,
) (string, string) {
	raw := strings.TrimSpace(cliSessionFile)
	if raw == "" {
		raw = strings.TrimSpace(envSessionFile)
	}
	if raw != "" {
		return resolveWorkDir(spec, cliSessionFile, envSessionFile)
	}

	// Try unified askd daemon state
	daemonWorkDir := runtime.GetDaemonWorkDir("cxb-askd.json")
	if daemonWorkDir != "" {
		if _, err := os.Stat(daemonWorkDir); err == nil {
			found := sessionutil.FindProjectSessionFile(daemonWorkDir, spec.SessionFilename)
			if found != "" {
				return daemonWorkDir, found
			}
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	pid := ""
	func() {
		defer func() { recover() }()
		pid = projectid.ComputeCURDXProjectID(cwd)
	}()
	if pid != "" {
		rec := paneregistry.LoadRegistryByProjectID(pid, provider)
		if rec != nil {
			sessionFile := ""
			if providersRaw, ok := rec["providers"]; ok {
				if provMap, ok := providersRaw.(map[string]interface{}); ok {
					if entry, ok := provMap[strings.ToLower(provider)]; ok {
						if entryMap, ok := entry.(map[string]interface{}); ok {
							if sf, ok := entryMap["session_file"].(string); ok && strings.TrimSpace(sf) != "" {
								sessionFile = strings.TrimSpace(sf)
							}
						}
					}
				}
			}
			if sessionFile == "" {
				if wd, ok := rec["work_dir"].(string); ok && strings.TrimSpace(wd) != "" {
					wdStr := strings.TrimSpace(wd)
					found := sessionutil.FindProjectSessionFile(wdStr, spec.SessionFilename)
					if found != "" {
						sessionFile = found
					} else {
						cfgDir := sessionutil.ResolveProjectConfigDir(wdStr)
						sessionFile = filepath.Join(cfgDir, spec.SessionFilename)
					}
				}
			}
			if sessionFile != "" {
				wd, sf := func() (string, string) {
					defer func() { recover() }()
					return resolveWorkDir(spec, sessionFile, "")
				}()
				if wd != "" {
					return wd, sf
				}
			}
		}
	}

	if envutil.EnvBool("CURDX_REGISTRY_ONLY", false) {
		panic(fmt.Sprintf("CURDX_REGISTRY_ONLY=1: registry routing failed for provider=%q cwd=%s", provider, cwd))
	}

	return cwd, ""
}

// -- Daemon communication --

type daemonResponse struct {
	reply    string
	exitCode int
}

func stateFileFromEnv(envName string) string {
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			raw = filepath.Join(home, raw[1:])
		}
	}
	return raw
}

func readState(stateFile string) map[string]interface{} {
	if stateFile == "" {
		return nil
	}
	return rpc.ReadState(stateFile)
}

func tryDaemonRequest(
	spec providers.ProviderClientSpec,
	workDir string,
	message string,
	timeout float64,
	quiet bool,
	stateFile string,
	outputPath string,
) *daemonResponse {
	if !envutil.EnvBool(spec.EnabledEnv, true) {
		return nil
	}
	if sessionutil.FindProjectSessionFile(workDir, spec.SessionFilename) == "" {
		return nil
	}

	st := readState(stateFile)

	// If state not found and CURDX_RUN_DIR is set, try project-specific state file
	if st == nil {
		runDir := strings.TrimSpace(os.Getenv("CURDX_RUN_DIR"))
		if runDir != "" {
			stateFilename := spec.ProtocolPrefix + "d.json"
			projectState := filepath.Join(runDir, stateFilename)
			if _, err := os.Stat(projectState); err == nil {
				st = readState(projectState)
			}
		}
	}

	// Also try the default state file path
	if st == nil {
		defaultPath := runtime.StateFilePath(spec.ProtocolPrefix + "d.json")
		st = readState(defaultPath)
	}

	if st == nil {
		return nil
	}

	host := ""
	if h, ok := st["connect_host"].(string); ok && h != "" {
		host = h
	}
	if host == "" {
		if h, ok := st["host"].(string); ok && h != "" {
			host = h
		}
	}
	if host == "" {
		return nil
	}

	portVal, ok := st["port"]
	if !ok {
		return nil
	}
	port := 0
	switch v := portVal.(type) {
	case float64:
		port = int(v)
	default:
		return nil
	}

	token, ok := st["token"].(string)
	if !ok || token == "" {
		return nil
	}

	payload := map[string]interface{}{
		"type":      spec.ProtocolPrefix + ".request",
		"v":         1,
		"id":        fmt.Sprintf("%s-%d-%d", spec.ProtocolPrefix, os.Getpid(), time.Now().UnixMilli()),
		"token":     token,
		"work_dir":  workDir,
		"timeout_s": timeout,
		"quiet":     quiet,
		"message":   message,
	}
	if outputPath != "" {
		payload["output_path"] = outputPath
	}
	if reqID := strings.TrimSpace(os.Getenv("CURDX_REQ_ID")); reqID != "" {
		payload["req_id"] = reqID
	}
	if noWrap := strings.TrimSpace(os.Getenv("CURDX_NO_WRAP")); noWrap == "1" || noWrap == "true" || noWrap == "yes" {
		payload["no_wrap"] = true
	}
	if caller := strings.TrimSpace(os.Getenv("CURDX_CALLER")); caller != "" {
		payload["caller"] = caller
	}

	reqBytes, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	reqBytes = append(reqBytes, '\n')

	connectTimeout := time.Duration(math.Min(5.0, math.Max(0.5, timeout)) * float64(time.Second))
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), connectTimeout)
	if err != nil {
		return nil
	}
	defer conn.Close()

	conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := conn.Write(reqBytes); err != nil {
		return nil
	}

	var buf []byte
	var deadline time.Time
	if timeout < 0 {
		// No deadline for negative timeout
		deadline = time.Time{}
	} else {
		deadline = time.Now().Add(time.Duration((timeout+5.0)*float64(time.Second)))
	}

	for {
		if !deadline.IsZero() && time.Now().After(deadline) {
			break
		}
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		tmp := make([]byte, 65536)
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if idx := indexOf(buf, '\n'); idx >= 0 {
			break
		}
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			break
		}
	}

	idx := indexOf(buf, '\n')
	if idx < 0 {
		return nil
	}
	line := string(buf[:idx])

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return nil
	}

	respType, _ := resp["type"].(string)
	if respType != spec.ProtocolPrefix+".response" {
		return nil
	}

	reply := ""
	if r, ok := resp["reply"].(string); ok {
		reply = r
	}
	exitCode := 1
	if ec, ok := resp["exit_code"].(float64); ok {
		exitCode = int(ec)
	}

	return &daemonResponse{reply: reply, exitCode: exitCode}
}

func indexOf(buf []byte, b byte) int {
	for i, c := range buf {
		if c == b {
			return i
		}
	}
	return -1
}

// -- Daemon auto-start --

func autostartEnabled(primaryEnv, legacyEnv string) bool {
	if _, ok := os.LookupEnv(primaryEnv); ok {
		return envutil.EnvBool(primaryEnv, true)
	}
	if _, ok := os.LookupEnv(legacyEnv); ok {
		return envutil.EnvBool(legacyEnv, true)
	}
	return true
}

func maybeStartDaemon(spec providers.ProviderClientSpec, workDir string) bool {
	if !envutil.EnvBool(spec.EnabledEnv, true) {
		return false
	}
	if !autostartEnabled(spec.AutostartEnvPrimary, spec.AutostartEnvLegacy) {
		return false
	}
	if sessionutil.FindProjectSessionFile(workDir, spec.SessionFilename) == "" {
		return false
	}

	// Find the daemon binary
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	binDir := filepath.Dir(exe)

	var candidates []string
	local := filepath.Join(binDir, spec.DaemonBinName)
	if _, err := os.Stat(local); err == nil {
		candidates = append(candidates, local)
	}
	// Also look for the Go binary
	goLocal := filepath.Join(binDir, spec.DaemonBinName)
	if _, err := os.Stat(goLocal); err == nil && goLocal != local {
		candidates = append(candidates, goLocal)
	}

	if len(candidates) == 0 {
		return false
	}

	entry := candidates[0]
	return startDetachedProcess(entry)
}

func waitForDaemonReady(spec providers.ProviderClientSpec, timeoutS float64, stateFile string) bool {
	if stateFile == "" {
		stateFile = stateFileFromEnv(spec.StateFileEnv)
	}
	if stateFile == "" {
		stateFile = runtime.StateFilePath(spec.ProtocolPrefix + "d.json")
	}

	deadline := time.Now().Add(time.Duration(math.Max(0.1, timeoutS) * float64(time.Second)))
	for time.Now().Before(deadline) {
		if rpc.PingDaemon(spec.ProtocolPrefix, 0.2, stateFile) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// -- Retry loop for gask/oask --

func daemonStartupWaitS(cfg ProviderCLIConfig, timeout float64) float64 {
	if cfg.StartupWaitEnv != "" {
		raw := strings.TrimSpace(os.Getenv(cfg.StartupWaitEnv))
		if raw != "" {
			v, err := strconv.ParseFloat(raw, 64)
			if err == nil && v > 0 {
				return math.Min(math.Max(0.2, v), math.Max(0.2, timeout))
			}
		}
	}
	return math.Min(8.0, math.Max(1.0, timeout))
}

func daemonRetryWaitS(cfg ProviderCLIConfig, timeout float64) float64 {
	if cfg.RetryWaitEnv != "" {
		raw := strings.TrimSpace(os.Getenv(cfg.RetryWaitEnv))
		if raw != "" {
			v, err := strconv.ParseFloat(raw, 64)
			if err == nil && v > 0 {
				return math.Min(1.0, math.Max(0.05, v))
			}
		}
	}
	return math.Min(0.3, math.Max(0.05, timeout/50.0))
}

func daemonRequestWithRetries(
	cfg ProviderCLIConfig,
	workDir string,
	message string,
	timeout float64,
	quiet bool,
	outputPath string,
) *daemonResponse {
	stateFile := stateFileFromEnv(cfg.Spec.StateFileEnv)

	// Fast path
	result := tryDaemonRequest(cfg.Spec, workDir, message, timeout, quiet, stateFile, outputPath)
	if result != nil {
		return result
	}

	if !envutil.EnvBool(cfg.Spec.EnabledEnv, true) {
		return nil
	}
	if sessionutil.FindProjectSessionFile(workDir, cfg.Spec.SessionFilename) == "" {
		return nil
	}

	// Stale state files can block daemon mode
	if stateFile != "" {
		if _, err := os.Stat(stateFile); err == nil {
			func() {
				defer func() { recover() }()
				if !waitForDaemonReady(cfg.Spec, 0.2, stateFile) {
					os.Remove(stateFile)
				}
			}()
		}
	}

	started := maybeStartDaemon(cfg.Spec, workDir)
	if started {
		waitForDaemonReady(cfg.Spec, daemonStartupWaitS(cfg, timeout), stateFile)
	}

	waitS := daemonRetryWaitS(cfg, timeout)
	retryDeadline := time.Now().Add(time.Duration(math.Min(3.0, math.Max(0.2, timeout)) * float64(time.Second)))
	for time.Now().Before(retryDeadline) {
		result = tryDaemonRequest(cfg.Spec, workDir, message, timeout, quiet, stateFile, outputPath)
		if result != nil {
			return result
		}
		time.Sleep(time.Duration(waitS * float64(time.Second)))
	}

	return nil
}

// -- Supervisor mode (cask only) --

const supervisorPrompt = `## Executor Mode: codex+opencode
You are the SUPERVISOR, NOT the executor.
- Do NOT directly edit repo files yourself.
- Break down tasks into clear instructions for OpenCode.
- Use cxb-opencode-ask to delegate execution to OpenCode.
- Review OpenCode results and iterate if needed.

`

func getExecutorFromRoles() string {
	home, _ := os.UserHomeDir()

	candidates := []string{
		".autoflow/roles.session.json",
		".autoflow/roles.json",
		filepath.Join(home, ".config", "curdx", "roles.json"),
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "curdx", "roles.json"))
	}

	for _, cfgPath := range candidates {
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal(data, &obj); err != nil {
			continue
		}
		if executor, ok := obj["executor"].(string); ok && executor != "" {
			return executor
		}
	}
	return ""
}

// -- Helpers --

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// ── Ping Commands ──

// ProviderPingConfig holds configuration for a provider ping command.
type ProviderPingConfig struct {
	ProgName        string
	ProviderLabel   string
	SessionFilename string
}

// RunPing implements the shared ping logic for provider-specific ping commands.
func RunPing(cfg ProviderPingConfig) int {
	fs := flag.NewFlagSet(cfg.ProgName, flag.ContinueOnError)
	sessionFileFlag := fs.String("session-file", "", "Path to session file")
	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		fmt.Fprintf(os.Stderr, "[ERROR] %s\n", err)
		return 1
	}
	if *sessionFileFlag != "" {
		os.Setenv("CURDX_SESSION_FILE", *sessionFileFlag)
	}
	healthy, message := checkPingHealth(cfg, *sessionFileFlag)
	fmt.Println(message)
	if healthy {
		return 0
	}
	return 1
}

func checkPingHealth(cfg ProviderPingConfig, sfOverride string) (bool, string) {
	sfPath := sfOverride
	if sfPath == "" {
		sfPath = os.Getenv("CURDX_SESSION_FILE")
	}
	if sfPath == "" {
		wd, _ := os.Getwd()
		sfPath = sessionutil.FindProjectSessionFile(wd, cfg.SessionFilename)
	}
	if sfPath == "" {
		return false, fmt.Sprintf("[%s] No session file found", cfg.ProviderLabel)
	}
	data, err := readSessionJSON(sfPath)
	if err != nil {
		return false, fmt.Sprintf("[%s] Cannot read session: %s", cfg.ProviderLabel, err)
	}
	active, _ := data["active"].(bool)
	if !active {
		return false, fmt.Sprintf("[%s] Session not active", cfg.ProviderLabel)
	}
	paneID := ""
	if v, ok := data["pane_id"].(string); ok && strings.TrimSpace(v) != "" {
		paneID = strings.TrimSpace(v)
	}
	if paneID == "" {
		if v, ok := data["tmux_session"].(string); ok && strings.TrimSpace(v) != "" {
			paneID = strings.TrimSpace(v)
		}
	}
	if paneID == "" {
		return false, fmt.Sprintf("[%s] Session ID not found", cfg.ProviderLabel)
	}
	return true, fmt.Sprintf("[%s] Connection OK (Session OK)", cfg.ProviderLabel)
}

// ResolveWorkDir derives work directory from a session file path.
func ResolveWorkDir(sessionFile string) string {
	if strings.TrimSpace(sessionFile) == "" {
		wd, _ := os.Getwd()
		return wd
	}
	abs, err := filepath.Abs(sessionFile)
	if err == nil {
		sessionFile = abs
	}
	parent := filepath.Base(filepath.Dir(sessionFile))
	if parent == sessionutil.CURDXProjectConfigDirname || parent == sessionutil.CURDXProjectConfigLegacyDirname {
		return filepath.Dir(filepath.Dir(sessionFile))
	}
	return filepath.Dir(sessionFile)
}

// ── Pend Commands ──

func readSessionJSON(path string) (map[string]interface{}, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		raw = raw[3:]
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// ProviderPendConfig holds the configuration for a provider pend command.
type ProviderPendConfig struct {
	ProgName        string
	ProviderLabel   string
	SessionFilename string
	LogPathKey      string // JSON key in session file for log path
}

// RunPend implements the shared pend logic for all provider-specific pend commands.
func RunPend(cfg ProviderPendConfig) int {
	fs := flag.NewFlagSet(cfg.ProgName, flag.ContinueOnError)
	sessionFileFlag := fs.String("session-file", "", "Path to session file")
	n := fs.Int("n", 1, "Number of conversations to show")
	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		fmt.Fprintf(os.Stderr, "[ERROR] %s\n", err)
		return 1
	}

	// Accept N as positional arg
	if *n == 1 && fs.NArg() > 0 {
		var parsed int
		if _, err := fmt.Sscanf(fs.Arg(0), "%d", &parsed); err == nil && parsed > 0 {
			*n = parsed
		}
	}

	if *sessionFileFlag != "" {
		os.Setenv("CURDX_SESSION_FILE", *sessionFileFlag)
	}

	sfPath := *sessionFileFlag
	if sfPath == "" {
		sfPath = os.Getenv("CURDX_SESSION_FILE")
	}
	if sfPath == "" {
		wd, _ := os.Getwd()
		sfPath = sessionutil.FindProjectSessionFile(wd, cfg.SessionFilename)
	}
	if sfPath == "" {
		fmt.Fprintf(os.Stderr, "No %s reply available (no session file)\n", cfg.ProviderLabel)
		return 2
	}

	data, err := readSessionJSON(sfPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No %s reply available: %s\n", cfg.ProviderLabel, err)
		return 2
	}

	logPath := ""
	if v, ok := data[cfg.LogPathKey].(string); ok {
		logPath = strings.TrimSpace(v)
	}
	if logPath == "" {
		fmt.Fprintf(os.Stderr, "No %s reply available (no log path in session)\n", cfg.ProviderLabel)
		return 2
	}

	content, err := os.ReadFile(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No %s reply available: %s\n", cfg.ProviderLabel, err)
		return 2
	}

	text := protocol.StripTrailingMarkers(string(content))
	if strings.TrimSpace(text) == "" {
		fmt.Fprintf(os.Stderr, "No %s reply available\n", cfg.ProviderLabel)
		return 2
	}

	fmt.Println(text)
	_ = *n // TODO: full N-conversation support
	return 0
}
