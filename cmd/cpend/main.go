// cpend — View latest Codex reply.
// Source: claude_code_bridge/bin/cpend
package main

import (
	"os"
	"github.com/anthropics/curdx-bridge/internal/askcli"
)

func main() {
	os.Exit(askcli.RunPend(askcli.ProviderPendConfig{
		ProgName:        "cpend",
		ProviderLabel:   "Codex",
		SessionFilename: ".codex-session",
		LogPathKey:      "codex_session_path",
	}))
}
