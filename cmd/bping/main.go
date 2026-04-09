// bping - Test CodeBuddy connectivity.
// Source: claude_code_bridge/bin/bping
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
				fmt.Printf("[ERROR] CodeBuddy connectivity test failed: %v\n", r)
				os.Exit(1)
			}
		}()
		return askcli.RunPing(askcli.ProviderPingConfig{
			ProgName:        "bping",
			ProviderLabel:   "CodeBuddy",
			SessionFilename: ".codebuddy-session",
		})
	}()
	os.Exit(code)
}
