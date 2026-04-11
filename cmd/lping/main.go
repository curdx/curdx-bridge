// lping - Test Claude connectivity.
// Source: claude_code_bridge/bin/lping
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
				fmt.Printf("[ERROR] Claude connectivity test failed: %v\n", r)
				os.Exit(1)
			}
		}()
		return askcli.RunPing(askcli.ProviderPingConfig{
			ProgName:        "lping",
			ProviderLabel:   "Claude",
			SessionFilename: ".claude-session",
		})
	}()
	os.Exit(code)
}
