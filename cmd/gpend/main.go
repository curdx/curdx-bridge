// gpend — View latest Gemini reply.
// Source: claude_code_bridge/bin/gpend
package main

import (
	"os"
	"github.com/curdx/curdx-bridge/internal/askcli"
)

func main() {
	os.Exit(askcli.RunPend(askcli.ProviderPendConfig{
		ProgName:        "gpend",
		ProviderLabel:   "Gemini",
		SessionFilename: ".gemini-session",
		LogPathKey:      "gemini_log_path",
	}))
}
