// Package i18n provides internationalization support for CURDX.
// Source: claude_code_bridge/lib/i18n.py
//
// Language detection priority:
//  1. CURDX_LANG environment variable (zh/en/auto)
//  2. System locale (LANG/LC_ALL/LC_MESSAGES)
//  3. Default to English
package i18n

import (
	"os"
	"strings"
	"sync"
)

var (
	currentLang string
	langOnce    sync.Once
	langMu      sync.Mutex
)

// Messages maps language -> key -> message template.
var Messages = map[string]map[string]string{
	"en": {
		// Terminal detection
		"no_terminal_backend":       "No terminal backend detected (WezTerm or tmux)",
		"solutions":                 "Solutions:",
		"install_wezterm":           "Install WezTerm (recommended): https://wezfurlong.org/wezterm/",
		"or_install_tmux":           "Or install tmux",
		"tmux_installed_not_inside": "tmux is installed, but you're not inside a tmux session (run `tmux` first, then run `curdx` inside tmux)",
		"or_set_curdx_terminal":       "Or set CURDX_TERMINAL=wezterm and configure CODEX_WEZTERM_BIN",
		"tmux_not_installed":        "tmux not installed and WezTerm unavailable",
		"install_wezterm_or_tmux":   "Solution: Install WezTerm (recommended) or tmux",
		"creating_tmux_session":     "Creating tmux session: {session}",
		"attaching_to_tmux":         "Attaching to tmux session: {session}",

		// Startup messages
		"starting_backend":  "Starting {provider} backend ({terminal})...",
		"started_backend":   "{provider} started ({terminal}: {pane_id})",
		"unknown_provider":  "Unknown provider: {provider}",
		"resuming_session":  "Resuming {provider} session: {session_id}...",
		"no_history_fresh":  "No {provider} history found, starting fresh",
		"warmup":            "Warmup: {script}",
		"warmup_failed":     "Warmup failed: {provider}",

		// Claude
		"starting_claude":    "Starting Claude...",
		"resuming_claude":    "Resuming Claude session: {session_id}...",
		"no_claude_session":  "No local Claude session found, starting fresh",
		"session_id":         "Session ID: {session_id}",
		"runtime_dir":        "Runtime dir: {runtime_dir}",
		"active_backends":    "Active backends: {backends}",
		"available_commands":  "Available commands:",
		"codex_commands":     "cxb-codex-ask/cxb-codex-askd/cxb-codex-ping/cxb-codex-pend - Codex communication",
		"gemini_commands":    "cxb-gemini-ask/cxb-gemini-ping/cxb-gemini-pend - Gemini communication",
		"executing":          "Executing: {cmd}",
		"user_interrupted":   "User interrupted",
		"cleaning_up":        "Cleaning up session resources...",
		"cleanup_complete":   "Cleanup complete",

		// Banner
		"banner_title":    "CURDX Bridge {version}",
		"banner_date":     "{date}",
		"banner_backends": "Backends: {backends}",

		// Errors
		"cannot_write_session": "Cannot write {filename}: {reason}",
		"fix_hint":             "Fix: {fix}",
		"error":                "Error",
		"execution_failed":     "Execution failed: {error}",
		"import_failed":        "Import failed: {error}",
		"module_import_failed": "Module import failed: {error}",

		// Connectivity
		"connectivity_test_failed": "{provider} connectivity test failed: {error}",
		"no_reply_available":       "No {provider} reply available",

		// Commands
		"usage":             "Usage: {cmd}",
		"sending_to":        "Sending question to {provider}...",
		"waiting_for_reply": "Waiting for {provider} reply (no timeout, Ctrl-C to interrupt)...",
		"reply_from":        "{provider} reply:",
		"timeout_no_reply":  "Timeout: no reply from {provider}",
		"session_not_found": "No active {provider} session found",

		// Install messages
		"install_complete":   "Installation complete",
		"uninstall_complete": "Uninstall complete",
		"python_version_old": "Python version too old: {version}",
		"requires_python":    "Requires Python 3.10+",
		"missing_dependency": "Missing dependency: {dep}",
		"detected_env":       "Detected {env} environment",
		"confirm_continue":   "Confirm continue? (y/N)",
		"cancelled":          "Cancelled",
	},
	"zh": {
		// Terminal detection
		"no_terminal_backend":       "未检测到终端后端 (WezTerm 或 tmux)",
		"solutions":                 "解决方案：",
		"install_wezterm":           "安装 WezTerm (推荐): https://wezfurlong.org/wezterm/",
		"or_install_tmux":           "或安装 tmux",
		"tmux_installed_not_inside": "已安装 tmux，但当前不在 tmux 会话中（请先运行 `tmux`，再在 tmux 内执行 `curdx`）",
		"or_set_curdx_terminal":       "或设置 CURDX_TERMINAL=wezterm 并配置 CODEX_WEZTERM_BIN",
		"tmux_not_installed":        "tmux 未安装且 WezTerm 不可用",
		"install_wezterm_or_tmux":   "解决方案：安装 WezTerm (推荐) 或 tmux",
		"creating_tmux_session":     "正在创建 tmux 会话: {session}",
		"attaching_to_tmux":         "正在连接到 tmux 会话: {session}",

		// Startup messages
		"starting_backend":  "正在启动 {provider} 后端 ({terminal})...",
		"started_backend":   "{provider} 已启动 ({terminal}: {pane_id})",
		"unknown_provider":  "未知提供者: {provider}",
		"resuming_session":  "正在恢复 {provider} 会话: {session_id}...",
		"no_history_fresh":  "未找到 {provider} 历史记录，全新启动",
		"warmup":            "预热: {script}",
		"warmup_failed":     "预热失败: {provider}",

		// Claude
		"starting_claude":    "正在启动 Claude...",
		"resuming_claude":    "正在恢复 Claude 会话: {session_id}...",
		"no_claude_session":  "未找到本地 Claude 会话，全新启动",
		"session_id":         "会话 ID: {session_id}",
		"runtime_dir":        "运行目录: {runtime_dir}",
		"active_backends":    "活动后端: {backends}",
		"available_commands":  "可用命令：",
		"codex_commands":     "cxb-codex-ask/cxb-codex-askd/cxb-codex-ping/cxb-codex-pend - Codex 通信",
		"gemini_commands":    "cxb-gemini-ask/cxb-gemini-ping/cxb-gemini-pend - Gemini 通信",
		"executing":          "执行: {cmd}",
		"user_interrupted":   "用户中断",
		"cleaning_up":        "正在清理会话资源...",
		"cleanup_complete":   "清理完成",

		// Banner
		"banner_title":    "CURDX Bridge {version}",
		"banner_date":     "{date}",
		"banner_backends": "后端: {backends}",

		// Errors
		"cannot_write_session": "无法写入 {filename}: {reason}",
		"fix_hint":             "修复: {fix}",
		"error":                "错误",
		"execution_failed":     "执行失败: {error}",
		"import_failed":        "导入失败: {error}",
		"module_import_failed": "模块导入失败: {error}",

		// Connectivity
		"connectivity_test_failed": "{provider} 连通性测试失败: {error}",
		"no_reply_available":       "暂无 {provider} 回复",

		// Commands
		"usage":             "用法: {cmd}",
		"sending_to":        "正在发送问题到 {provider}...",
		"waiting_for_reply": "等待 {provider} 回复 (无超时，Ctrl-C 中断)...",
		"reply_from":        "{provider} 回复:",
		"timeout_no_reply":  "超时: 未收到 {provider} 回复",
		"session_not_found": "未找到活动的 {provider} 会话",

		// Install messages
		"install_complete":   "安装完成",
		"uninstall_complete": "卸载完成",
		"python_version_old": "Python 版本过旧: {version}",
		"requires_python":    "需要 Python 3.10+",
		"missing_dependency": "缺少依赖: {dep}",
		"detected_env":       "检测到 {env} 环境",
		"confirm_continue":   "确认继续？(y/N)",
		"cancelled":          "已取消",
	},
}

// DetectLanguage detects language from environment.
func DetectLanguage() string {
	curdxLang := strings.ToLower(os.Getenv("CURDX_LANG"))
	if curdxLang == "" {
		curdxLang = "auto"
	}

	switch curdxLang {
	case "zh", "cn", "chinese":
		return "zh"
	case "en", "english":
		return "en"
	}

	// Auto-detect from system locale
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = os.Getenv("LC_ALL")
	}
	if lang == "" {
		lang = os.Getenv("LC_MESSAGES")
	}

	lower := strings.ToLower(lang)
	if strings.HasPrefix(lower, "zh") || strings.Contains(lower, "chinese") {
		return "zh"
	}

	return "en"
}

// GetLang returns the current language setting.
func GetLang() string {
	langMu.Lock()
	defer langMu.Unlock()
	if currentLang == "" {
		currentLang = DetectLanguage()
	}
	return currentLang
}

// SetLang sets the language explicitly.
func SetLang(lang string) {
	langMu.Lock()
	defer langMu.Unlock()
	if lang == "zh" || lang == "en" {
		currentLang = lang
	}
}

// T returns a translated message by key, with format arguments substituted.
// Uses Python-style {name} placeholders for compatibility.
func T(key string, kwargs map[string]string) string {
	lang := GetLang()
	messages, ok := Messages[lang]
	if !ok {
		messages = Messages["en"]
	}

	msg, ok := messages[key]
	if !ok {
		// Fallback to English
		msg, ok = Messages["en"][key]
		if !ok {
			return key
		}
	}

	if len(kwargs) > 0 {
		for k, v := range kwargs {
			msg = strings.ReplaceAll(msg, "{"+k+"}", v)
		}
	}

	return msg
}
