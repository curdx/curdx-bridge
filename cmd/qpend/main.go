// qpend — View latest Qwen reply.
// Source: claude_code_bridge/bin/qpend
package main

import (
	"os"
	"github.com/anthropics/curdx-bridge/internal/askcli"
)

func main() {
	os.Exit(askcli.RunPend(askcli.ProviderPendConfig{
		ProgName:        "qpend",
		ProviderLabel:   "Qwen",
		SessionFilename: ".qwen-session",
		LogPathKey:      "qwen_log_path",
	}))
}
