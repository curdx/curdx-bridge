// dpend — View latest Droid reply.
// Source: claude_code_bridge/bin/dpend
package main

import (
	"os"
	"github.com/anthropics/curdx-bridge/internal/askcli"
)

func main() {
	os.Exit(askcli.RunPend(askcli.ProviderPendConfig{
		ProgName:        "dpend",
		ProviderLabel:   "Droid",
		SessionFilename: ".droid-session",
		LogPathKey:      "droid_session_path",
	}))
}
