// bpend — View latest CodeBuddy reply.
// Source: claude_code_bridge/bin/bpend
package main

import (
	"os"
	"github.com/anthropics/curdx-bridge/internal/askcli"
)

func main() {
	os.Exit(askcli.RunPend(askcli.ProviderPendConfig{
		ProgName:        "bpend",
		ProviderLabel:   "CodeBuddy",
		SessionFilename: ".codebuddy-session",
		LogPathKey:      "codebuddy_log_path",
	}))
}
