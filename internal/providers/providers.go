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
	IdleTimeoutEnv: "CURDX_CASKD_IDLE_TIMEOUT_S",
	LockName:       "caskd",
}

var GaskdSpec = ProviderDaemonSpec{
	DaemonKey:      "gaskd",
	ProtocolPrefix: "gask",
	StateFileName:  "gaskd.json",
	LogFileName:    "gaskd.log",
	IdleTimeoutEnv: "CURDX_GASKD_IDLE_TIMEOUT_S",
	LockName:       "gaskd",
}

var OaskdSpec = ProviderDaemonSpec{
	DaemonKey:      "oaskd",
	ProtocolPrefix: "oask",
	StateFileName:  "oaskd.json",
	LogFileName:    "oaskd.log",
	IdleTimeoutEnv: "CURDX_OASKD_IDLE_TIMEOUT_S",
	LockName:       "oaskd",
}

var LaskdSpec = ProviderDaemonSpec{
	DaemonKey:      "laskd",
	ProtocolPrefix: "lask",
	StateFileName:  "laskd.json",
	LogFileName:    "laskd.log",
	IdleTimeoutEnv: "CURDX_LASKD_IDLE_TIMEOUT_S",
	LockName:       "laskd",
}

// Client specs

var CaskClientSpec = ProviderClientSpec{
	ProtocolPrefix:      "cask",
	EnabledEnv:          "CURDX_CASKD",
	AutostartEnvPrimary: "CURDX_CASKD_AUTOSTART",
	AutostartEnvLegacy:  "CURDX_AUTO_CASKD",
	StateFileEnv:        "CURDX_CASKD_STATE_FILE",
	SessionFilename:     ".codex-session",
	DaemonBinName:       "askd",
	DaemonModule:        "askd.daemon",
}

var GaskClientSpec = ProviderClientSpec{
	ProtocolPrefix:      "gask",
	EnabledEnv:          "CURDX_GASKD",
	AutostartEnvPrimary: "CURDX_GASKD_AUTOSTART",
	AutostartEnvLegacy:  "CURDX_AUTO_GASKD",
	StateFileEnv:        "CURDX_GASKD_STATE_FILE",
	SessionFilename:     ".gemini-session",
	DaemonBinName:       "askd",
	DaemonModule:        "askd.daemon",
}

var OaskClientSpec = ProviderClientSpec{
	ProtocolPrefix:      "oask",
	EnabledEnv:          "CURDX_OASKD",
	AutostartEnvPrimary: "CURDX_OASKD_AUTOSTART",
	AutostartEnvLegacy:  "CURDX_AUTO_OASKD",
	StateFileEnv:        "CURDX_OASKD_STATE_FILE",
	SessionFilename:     ".opencode-session",
	DaemonBinName:       "askd",
	DaemonModule:        "askd.daemon",
}

var LaskClientSpec = ProviderClientSpec{
	ProtocolPrefix:      "lask",
	EnabledEnv:          "CURDX_LASKD",
	AutostartEnvPrimary: "CURDX_LASKD_AUTOSTART",
	AutostartEnvLegacy:  "CURDX_AUTO_LASKD",
	StateFileEnv:        "CURDX_LASKD_STATE_FILE",
	SessionFilename:     ".claude-session",
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
