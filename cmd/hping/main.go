// hping - Test Copilot connectivity.
// Source: claude_code_bridge/bin/hping
package main

import (
	"fmt"
	"os"

	"github.com/anthropics/curdx-bridge/internal/askcli"
)

func main() {
	code := func() int {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("[ERROR] Copilot connectivity test failed: %v\n", r)
				os.Exit(1)
			}
		}()
		return askcli.RunPing(askcli.ProviderPingConfig{
			ProgName:        "hping",
			ProviderLabel:   "Copilot",
			SessionFilename: ".copilot-session",
		})
	}()
	os.Exit(code)
}
