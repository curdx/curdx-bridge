// qping - Test Qwen connectivity.
// Source: claude_code_bridge/bin/qping
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
				fmt.Printf("[ERROR] Qwen connectivity test failed: %v\n", r)
				os.Exit(1)
			}
		}()
		return askcli.RunPing(askcli.ProviderPingConfig{
			ProgName:        "qping",
			ProviderLabel:   "Qwen",
			SessionFilename: ".qwen-session",
		})
	}()
	os.Exit(code)
}
