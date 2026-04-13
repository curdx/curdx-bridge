// opend — View latest OpenCode reply.
// Source: claude_code_bridge/bin/opend
package main

import (
	"os"
	"github.com/curdx/curdx-bridge/internal/askcli"
)

func main() {
	os.Exit(askcli.RunPend(askcli.ProviderPendConfig{
		ProgName:        "cxb-opencode-pend",
		ProviderLabel:   "OpenCode",
		SessionFilename: ".opencode-session",
		LogPathKey:      "opencode_log_path",
	}))
}
