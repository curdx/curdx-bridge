// oping - Test OpenCode connectivity.
// Source: claude_code_bridge/bin/oping
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
				fmt.Printf("[ERROR] OpenCode connectivity test failed: %v\n", r)
				os.Exit(1)
			}
		}()
		return askcli.RunPing(askcli.ProviderPingConfig{
			ProgName:        "cxb-opencode-ping",
			ProviderLabel:   "OpenCode",
			SessionFilename: ".opencode-session",
		})
	}()
	os.Exit(code)
}
