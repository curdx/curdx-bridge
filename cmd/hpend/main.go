// hpend — View latest Copilot reply.
// Source: claude_code_bridge/bin/hpend
package main

import (
	"os"
	"github.com/anthropics/curdx-bridge/internal/askcli"
)

func main() {
	os.Exit(askcli.RunPend(askcli.ProviderPendConfig{
		ProgName:        "hpend",
		ProviderLabel:   "Copilot",
		SessionFilename: ".copilot-session",
		LogPathKey:      "copilot_log_path",
	}))
}
