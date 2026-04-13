package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/curdx/curdx-bridge/internal/memory"
)

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: cxb-ctx-transfer [OPTIONS]

Transfer conversation context between CURDX providers.

Options:
  -n, --last N         Number of conversation pairs (default: 3)
  --from, --source P   Source provider: auto|claude|codex|gemini|opencode (default: auto)
  -t, --to P           Target provider: codex|gemini|opencode (default: codex)
  --send               Send to provider via ask (default: disabled)
  -d, --dry-run        Preview output without sending
  -o, --output PATH    Write output to file
  --session-path PATH  Explicit session JSONL path
  --max-tokens N       Maximum tokens to transfer (default: 8000)
  -f, --format FMT     Output format: markdown|plain|json (default: markdown)
  -q, --quiet          Suppress informational output
  -s, --save           Save transfer to ./.curdx/history/
  --no-save            Disable auto-save when sending
  --detailed           Output detailed tool executions
  -h, --help           Show this help message`)
}

type args struct {
	lastN          int
	sourceProvider string
	provider       string
	send           bool
	dryRun         bool
	outputPath     string
	sessionPath    string
	maxTokens      int
	format         string
	quiet          bool
	save           bool
	noSave         bool
	detailed       bool
}

var validSourceProviders = map[string]bool{
	"auto": true, "claude": true, "codex": true, "gemini": true, "opencode": true,
}

var validTargetProviders = map[string]bool{
	"codex": true, "gemini": true, "opencode": true,
}

var validFormats = map[string]bool{
	"markdown": true, "plain": true, "json": true,
}

func parseArgs(argv []string) (*args, error) {
	a := &args{
		lastN:          3,
		sourceProvider: "auto",
		provider:       "codex",
		maxTokens:      8000,
		format:         "markdown",
	}

	for i := 1; i < len(argv); i++ {
		tok := argv[i]
		switch tok {
		case "-h", "--help":
			usage()
			os.Exit(0)
		case "-n", "--last":
			i++
			if i >= len(argv) {
				return nil, fmt.Errorf("--last requires a number")
			}
			v, err := strconv.Atoi(argv[i])
			if err != nil {
				return nil, fmt.Errorf("--last must be a number")
			}
			a.lastN = v
		case "--from", "--source":
			i++
			if i >= len(argv) {
				return nil, fmt.Errorf("--from requires a provider name")
			}
			val := strings.ToLower(argv[i])
			if !validSourceProviders[val] {
				return nil, fmt.Errorf("invalid source provider: %s", val)
			}
			a.sourceProvider = val
		case "-t", "--to":
			i++
			if i >= len(argv) {
				return nil, fmt.Errorf("--to requires a provider name")
			}
			val := strings.ToLower(argv[i])
			if !validTargetProviders[val] {
				return nil, fmt.Errorf("invalid target provider: %s", val)
			}
			a.provider = val
		case "--send":
			a.send = true
		case "-d", "--dry-run":
			a.dryRun = true
		case "-o", "--output":
			i++
			if i >= len(argv) {
				return nil, fmt.Errorf("--output requires a path")
			}
			a.outputPath = argv[i]
		case "--session-path":
			i++
			if i >= len(argv) {
				return nil, fmt.Errorf("--session-path requires a path")
			}
			a.sessionPath = argv[i]
		case "--max-tokens":
			i++
			if i >= len(argv) {
				return nil, fmt.Errorf("--max-tokens requires a number")
			}
			v, err := strconv.Atoi(argv[i])
			if err != nil {
				return nil, fmt.Errorf("--max-tokens must be a number")
			}
			a.maxTokens = v
		case "-f", "--format":
			i++
			if i >= len(argv) {
				return nil, fmt.Errorf("--format requires a value")
			}
			val := strings.ToLower(argv[i])
			if !validFormats[val] {
				return nil, fmt.Errorf("invalid format: %s", val)
			}
			a.format = val
		case "-q", "--quiet":
			a.quiet = true
		case "-s", "--save":
			a.save = true
		case "--no-save":
			a.noSave = true
		case "--detailed":
			a.detailed = true
		default:
			return nil, fmt.Errorf("unknown argument: %s", tok)
		}
	}
	return a, nil
}

func main() {
	os.Exit(run(os.Args))
}

func run(argv []string) int {
	a, err := parseArgs(argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	// Create session parser
	parser := memory.NewClaudeSessionParser("")

	// Resolve session
	sessionPath, err := parser.ResolveSession(cwd, a.sessionPath)
	if err != nil {
		if _, ok := err.(*memory.SessionNotFoundError); ok {
			fmt.Fprintf(os.Stderr, "Session not found: %v\n", err)
			fmt.Fprintln(os.Stderr, "Hints:")
			fmt.Fprintln(os.Stderr, "  - Ensure a CURDX-supported CLI is running in this directory")
			fmt.Fprintln(os.Stderr, "  - Use --from to select a specific provider")
			fmt.Fprintln(os.Stderr, "  - Use --session-path to specify a Claude session file")
			return 1
		}
		if _, ok := err.(*memory.SessionParseError); ok {
			fmt.Fprintf(os.Stderr, "Session parse error: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// Parse session
	entries, err := parser.ParseSession(sessionPath)
	if err != nil {
		if _, ok := err.(*memory.SessionParseError); ok {
			fmt.Fprintf(os.Stderr, "Session parse error: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// Clean and deduplicate
	deduper := &memory.ConversationDeduper{}
	var cleaned []memory.ConversationEntry
	for _, entry := range entries {
		e := entry
		e.Content = deduper.CleanContent(e.Content)
		cleaned = append(cleaned, e)
	}
	cleaned = deduper.DedupeMessages(cleaned)
	if !a.detailed {
		cleaned = deduper.CollapseToolCalls(cleaned)
	}

	// Pair user/assistant messages
	var conversations [][2]string
	for i := 0; i < len(cleaned)-1; i++ {
		if cleaned[i].Role == "user" && i+1 < len(cleaned) && cleaned[i+1].Role == "assistant" {
			conversations = append(conversations, [2]string{cleaned[i].Content, cleaned[i+1].Content})
			i++ // skip the assistant entry
		}
	}

	if len(conversations) == 0 {
		fmt.Fprintln(os.Stderr, "No conversations found in session.")
		return 1
	}

	// Take last N
	if a.lastN > 0 && a.lastN < len(conversations) {
		conversations = conversations[len(conversations)-a.lastN:]
	}

	// Format output
	formatter := memory.NewContextFormatter(a.maxTokens)
	conversations = formatter.TruncateToLimit(conversations, a.maxTokens)

	sessionInfo := parser.GetSessionInfo(sessionPath)
	ctx := &memory.TransferContext{
		Conversations:   conversations,
		SourceSessionID: sessionInfo.SessionID,
		TokenEstimate:   formatter.EstimateTokens(flattenConversations(conversations)),
		SourceProvider:  a.sourceProvider,
	}

	if !a.quiet {
		provLabel := a.sourceProvider
		if provLabel == "" {
			provLabel = "auto"
		}
		fmt.Fprintf(os.Stderr, "Extracted %d conversation(s) (~%d tokens) from %s\n",
			len(ctx.Conversations), ctx.TokenEstimate, provLabel)
	}

	formatted := formatter.Format(ctx, a.format, a.detailed)

	// Handle output modes
	if a.dryRun {
		fmt.Println(formatted)
		return 0
	}

	if a.outputPath != "" {
		dir := filepath.Dir(a.outputPath)
		_ = os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(a.outputPath, []byte(formatted), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing output: %v\n", err)
			return 1
		}
		if !a.quiet {
			fmt.Fprintf(os.Stderr, "Written to %s\n", a.outputPath)
		}
		return 0
	}

	if !a.send {
		// Default: save to history dir
		savedPath := saveTransfer(cwd, formatted, a.format, "")
		if savedPath != "" && !a.quiet {
			fmt.Fprintf(os.Stderr, "Saved to %s\n", savedPath)
		}
		return 0
	}

	// Save if requested or sending to provider (unless --no-save)
	shouldSave := a.save || !a.noSave
	if shouldSave {
		savedPath := saveTransfer(cwd, formatted, a.format, a.provider)
		if savedPath != "" && !a.quiet {
			fmt.Fprintf(os.Stderr, "Saved to %s\n", savedPath)
		}
	}

	// Send to provider via ask
	success, result := sendToProvider(formatted, a.provider)
	if success {
		if !a.quiet {
			fmt.Fprintf(os.Stderr, "Sent to %s\n", a.provider)
		}
		if result != "" {
			fmt.Println(result)
		}
		return 0
	}
	fmt.Fprintf(os.Stderr, "Failed to send: %s\n", result)
	return 1
}

func flattenConversations(convs [][2]string) string {
	var sb strings.Builder
	for _, pair := range convs {
		sb.WriteString(pair[0])
		sb.WriteString(pair[1])
	}
	return sb.String()
}

func saveTransfer(workDir, content, format, targetProvider string) string {
	historyDir := filepath.Join(workDir, ".curdx", "history")
	_ = os.MkdirAll(historyDir, 0o755)

	ext := ".md"
	switch format {
	case "plain":
		ext = ".txt"
	case "json":
		ext = ".json"
	}

	ts := strings.ReplaceAll(strings.ReplaceAll(
		strings.Split(strings.ReplaceAll(
			fmt.Sprintf("%v", os.Getpid()), " ", ""), " ")[0],
		"-", ""), ":", "")
	_ = ts

	name := fmt.Sprintf("cxb-ctx-transfer-%d%s", os.Getpid(), ext)
	if targetProvider != "" {
		name = fmt.Sprintf("cxb-ctx-transfer-%s-%d%s", targetProvider, os.Getpid(), ext)
	}
	path := filepath.Join(historyDir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return ""
	}
	return path
}

func sendToProvider(content, provider string) (bool, string) {
	// Find ask command
	selfPath, _ := os.Executable()
	var askCmd string
	if selfPath != "" {
		askCmd = filepath.Join(filepath.Dir(selfPath), "cxb-ask")
		if _, err := os.Stat(askCmd); err != nil {
			askCmd = ""
		}
	}
	if askCmd == "" {
		if _, found := os.LookupEnv("PATH"); found {
			// Try PATH lookup
		}
		return false, "ask command not found"
	}

	cmd := exec.Command(askCmd, provider, "--foreground", "--no-wrap")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, string(out)
	}
	return true, strings.TrimSpace(string(out))
}
