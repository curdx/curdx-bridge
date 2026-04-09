// gping - Test Gemini connectivity.
// Source: claude_code_bridge/bin/gping
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
				fmt.Printf("[ERROR] Gemini connectivity test failed: %v\n", r)
				os.Exit(1)
			}
		}()
		return askcli.RunPing(askcli.ProviderPingConfig{
			ProgName:        "gping",
			ProviderLabel:   "Gemini",
			SessionFilename: ".gemini-session",
		})
	}()
	os.Exit(code)
}
