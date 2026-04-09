// Package providers defines provider daemon and client spec constants.
// Source: claude_code_bridge/lib/providers.py
package providers

import (
	"strings"
)

// ProviderDaemonSpec holds the daemon-side configuration for a provider.
type ProviderDaemonSpec struct {
	DaemonKey      string
	ProtocolPrefix string
	StateFileName  string
	LogFileName    string
	IdleTimeoutEnv string
	LockName       string
}

// ProviderClientSpec holds the client-side configuration for a provider.
type ProviderClientSpec struct {
	ProtocolPrefix      string
	EnabledEnv          string
	AutostartEnvPrimary string
	AutostartEnvLegacy  string
	StateFileEnv        string
	SessionFilename     string
	DaemonBinName       string
	DaemonModule        string
}

// Daemon specs

var CaskdSpec = ProviderDaemonSpec{
	DaemonKey:      "caskd",
	ProtocolPrefix: "cask",
	StateFileName:  "caskd.json",
	LogFileName:    "caskd.log",
	IdleTimeoutEnv: "CCB_CASKD_IDLE_TIMEOUT_S",
	LockName:       "caskd",
}

var GaskdSpec = ProviderDaemonSpec{
	DaemonKey:      "gaskd",
	ProtocolPrefix: "gask",
	StateFileName:  "gaskd.json",
	LogFileName:    "gaskd.log",
	IdleTimeoutEnv: "CCB_GASKD_IDLE_TIMEOUT_S",
	LockName:       "gaskd",
}

var OaskdSpec = ProviderDaemonSpec{
	DaemonKey:      "oaskd",
	ProtocolPrefix: "oask",
	StateFileName:  "oaskd.json",
	LogFileName:    "oaskd.log",
	IdleTimeoutEnv: "CCB_OASKD_IDLE_TIMEOUT_S",
	LockName:       "oaskd",
}

var LaskdSpec = ProviderDaemonSpec{
	DaemonKey:      "laskd",
	ProtocolPrefix: "lask",
	StateFileName:  "laskd.json",
	LogFileName:    "laskd.log",
	IdleTimeoutEnv: "CCB_LASKD_IDLE_TIMEOUT_S",
	LockName:       "laskd",
}

var DaskdSpec = ProviderDaemonSpec{
	DaemonKey:      "daskd",
	ProtocolPrefix: "dask",
	StateFileName:  "daskd.json",
	LogFileName:    "daskd.log",
	IdleTimeoutEnv: "CCB_DASKD_IDLE_TIMEOUT_S",
	LockName:       "daskd",
}

// Copilot (GitHub Copilot CLI)
var HaskdSpec = ProviderDaemonSpec{
	DaemonKey:      "haskd",
	ProtocolPrefix: "hask",
	StateFileName:  "haskd.json",
	LogFileName:    "haskd.log",
	IdleTimeoutEnv: "CCB_HASKD_IDLE_TIMEOUT_S",
	LockName:       "haskd",
}

// CodeBuddy (Tencent CodeBuddy CLI)
var BaskdSpec = ProviderDaemonSpec{
	DaemonKey:      "baskd",
	ProtocolPrefix: "bask",
	StateFileName:  "baskd.json",
	LogFileName:    "baskd.log",
	IdleTimeoutEnv: "CCB_BASKD_IDLE_TIMEOUT_S",
	LockName:       "baskd",
}

// Qwen (qwen-code CLI)
var QaskdSpec = ProviderDaemonSpec{
	DaemonKey:      "qaskd",
	ProtocolPrefix: "qask",
	StateFileName:  "qaskd.json",
	LogFileName:    "qaskd.log",
	IdleTimeoutEnv: "CCB_QASKD_IDLE_TIMEOUT_S",
	LockName:       "qaskd",
}

// Client specs

var CaskClientSpec = ProviderClientSpec{
	ProtocolPrefix:      "cask",
	EnabledEnv:          "CCB_CASKD",
	AutostartEnvPrimary: "CCB_CASKD_AUTOSTART",
	AutostartEnvLegacy:  "CCB_AUTO_CASKD",
	StateFileEnv:        "CCB_CASKD_STATE_FILE",
	SessionFilename:     ".codex-session",
	DaemonBinName:       "askd",
	DaemonModule:        "askd.daemon",
}

var GaskClientSpec = ProviderClientSpec{
	ProtocolPrefix:      "gask",
	EnabledEnv:          "CCB_GASKD",
	AutostartEnvPrimary: "CCB_GASKD_AUTOSTART",
	AutostartEnvLegacy:  "CCB_AUTO_GASKD",
	StateFileEnv:        "CCB_GASKD_STATE_FILE",
	SessionFilename:     ".gemini-session",
	DaemonBinName:       "askd",
	DaemonModule:        "askd.daemon",
}

var OaskClientSpec = ProviderClientSpec{
	ProtocolPrefix:      "oask",
	EnabledEnv:          "CCB_OASKD",
	AutostartEnvPrimary: "CCB_OASKD_AUTOSTART",
	AutostartEnvLegacy:  "CCB_AUTO_OASKD",
	StateFileEnv:        "CCB_OASKD_STATE_FILE",
	SessionFilename:     ".opencode-session",
	DaemonBinName:       "askd",
	DaemonModule:        "askd.daemon",
}

var LaskClientSpec = ProviderClientSpec{
	ProtocolPrefix:      "lask",
	EnabledEnv:          "CCB_LASKD",
	AutostartEnvPrimary: "CCB_LASKD_AUTOSTART",
	AutostartEnvLegacy:  "CCB_AUTO_LASKD",
	StateFileEnv:        "CCB_LASKD_STATE_FILE",
	SessionFilename:     ".claude-session",
	DaemonBinName:       "askd",
	DaemonModule:        "askd.daemon",
}

var DaskClientSpec = ProviderClientSpec{
	ProtocolPrefix:      "dask",
	EnabledEnv:          "CCB_DASKD",
	AutostartEnvPrimary: "CCB_DASKD_AUTOSTART",
	AutostartEnvLegacy:  "CCB_AUTO_DASKD",
	StateFileEnv:        "CCB_DASKD_STATE_FILE",
	SessionFilename:     ".droid-session",
	DaemonBinName:       "askd",
	DaemonModule:        "askd.daemon",
}

var HaskClientSpec = ProviderClientSpec{
	ProtocolPrefix:      "hask",
	EnabledEnv:          "CCB_HASKD",
	AutostartEnvPrimary: "CCB_HASKD_AUTOSTART",
	AutostartEnvLegacy:  "CCB_AUTO_HASKD",
	StateFileEnv:        "CCB_HASKD_STATE_FILE",
	SessionFilename:     ".copilot-session",
	DaemonBinName:       "askd",
	DaemonModule:        "askd.daemon",
}

var BaskClientSpec = ProviderClientSpec{
	ProtocolPrefix:      "bask",
	EnabledEnv:          "CCB_BASKD",
	AutostartEnvPrimary: "CCB_BASKD_AUTOSTART",
	AutostartEnvLegacy:  "CCB_AUTO_BASKD",
	StateFileEnv:        "CCB_BASKD_STATE_FILE",
	SessionFilename:     ".codebuddy-session",
	DaemonBinName:       "askd",
	DaemonModule:        "askd.daemon",
}

var QaskClientSpec = ProviderClientSpec{
	ProtocolPrefix:      "qask",
	EnabledEnv:          "CCB_QASKD",
	AutostartEnvPrimary: "CCB_QASKD_AUTOSTART",
	AutostartEnvLegacy:  "CCB_AUTO_QASKD",
	StateFileEnv:        "CCB_QASKD_STATE_FILE",
	SessionFilename:     ".qwen-session",
	DaemonBinName:       "askd",
	DaemonModule:        "askd.daemon",
}

// ParseQualifiedProvider parses "codex:auth" -> ("codex", "auth"); "codex" -> ("codex", "").
// An empty instance is returned as "" (Go equivalent of Python None).
func ParseQualifiedProvider(key string) (base, instance string) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return "", ""
	}
	parts := strings.SplitN(key, ":", 2)
	base = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		instance = strings.TrimSpace(parts[1])
	}
	return base, instance
}

// MakeQualifiedKey combines base provider and instance: "codex" + "auth" -> "codex:auth".
func MakeQualifiedKey(base, instance string) string {
	base = strings.ToLower(strings.TrimSpace(base))
	instance = strings.TrimSpace(instance)
	if instance != "" {
		return base + ":" + instance
	}
	return base
}

// SessionFilenameForInstance derives an instance-specific session filename.
// ".codex-session" + "auth" -> ".codex-auth-session"
// ".codex-session" + ""     -> ".codex-session"
func SessionFilenameForInstance(baseFilename, instance string) string {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return baseFilename
	}
	// Insert instance before "-session" suffix
	if strings.HasSuffix(baseFilename, "-session") {
		prefix := baseFilename[:len(baseFilename)-len("-session")]
		return prefix + "-" + instance + "-session"
	}
	// Fallback: append instance before extension
	return baseFilename + "-" + instance
}
