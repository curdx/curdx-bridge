// lask - Send message to Claude and wait for reply (sync).
// Source: claude_code_bridge/bin/lask
package main

import (
	"os"

	"github.com/anthropics/curdx-bridge/internal/askcli"
	"github.com/anthropics/curdx-bridge/internal/providers"
)

func main() {
	os.Exit(askcli.Run(askcli.ProviderCLIConfig{
		CmdName:      "lask",
		ProviderName: "Claude",
		ProviderKey:  "claude",
		Spec:         providers.LaskClientSpec,
		AsyncGuardrail: `[CCB_ASYNC_SUBMITTED provider=claude]
IMPORTANT: Task submitted to Claude. You MUST:
1. Tell user "Claude processing..."
2. END YOUR TURN IMMEDIATELY
3. Do NOT wait, poll, check status, or use any more tools
`,
		DefaultTimeout:         -1.0,
		HasRetryLoop:           false,
		HasAsyncMode:           true,
		DaemonHint:             "laskd",
		DaemonAutostartEnvHint: "CCB_LASKD_AUTOSTART=1",
		SetupHint:              "`ccb claude`",
	}))
}
