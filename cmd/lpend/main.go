// lpend — View latest Claude reply.
// Source: claude_code_bridge/bin/lpend
package main

import (
	"os"
	"github.com/anthropics/curdx-bridge/internal/askcli"
)

func main() {
	os.Exit(askcli.RunPend(askcli.ProviderPendConfig{
		ProgName:        "lpend",
		ProviderLabel:   "Claude",
		SessionFilename: ".claude-session",
		LogPathKey:      "claude_session_path",
	}))
}
