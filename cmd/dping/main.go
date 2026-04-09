// dping - Test Droid connectivity.
// Source: claude_code_bridge/bin/dping
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
				fmt.Printf("[ERROR] Droid connectivity test failed: %v\n", r)
				os.Exit(1)
			}
		}()
		return askcli.RunPing(askcli.ProviderPingConfig{
			ProgName:        "dping",
			ProviderLabel:   "Droid",
			SessionFilename: ".droid-session",
		})
	}()
	os.Exit(code)
}
