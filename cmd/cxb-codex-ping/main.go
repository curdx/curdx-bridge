// cping - Test Codex connectivity.
// Source: claude_code_bridge/bin/cping
package main

import (
	"fmt"
	"os"

	"github.com/curdx/curdx-bridge/internal/askcli"
)

func main() {
	code := func() int {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("[ERROR] Connectivity test failed: %v\n", r)
				os.Exit(1)
			}
		}()
		return askcli.RunPing(askcli.ProviderPingConfig{
			ProgName:        "cxb-codex-ping",
			ProviderLabel:   "Codex",
			SessionFilename: ".codex-session",
		})
	}()
	os.Exit(code)
}
