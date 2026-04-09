// oask - Send message to OpenCode and wait for reply (sync).
// Source: claude_code_bridge/bin/oask
package main

import (
	"os"

	"github.com/anthropics/curdx-bridge/internal/askcli"
	"github.com/anthropics/curdx-bridge/internal/providers"
)

func main() {
	os.Exit(askcli.Run(askcli.ProviderCLIConfig{
		CmdName:      "oask",
		ProviderName: "OpenCode",
		ProviderKey:  "opencode",
		Spec:         providers.OaskClientSpec,
		AsyncGuardrail: `[CCB_ASYNC_SUBMITTED provider=opencode]
IMPORTANT: Task submitted to OpenCode. You MUST:
1. Tell user "OpenCode processing..."
2. END YOUR TURN IMMEDIATELY
3. Do NOT wait, poll, check status, or use any more tools
`,
		DefaultTimeout:         3600.0,
		HasRetryLoop:           true,
		HasAsyncMode:           true,
		StartupWaitEnv:         "CCB_OASKD_STARTUP_WAIT_S",
		RetryWaitEnv:           "CCB_OASKD_RETRY_WAIT_S",
		DaemonHint:             "oaskd",
		DaemonAutostartEnvHint: "CCB_OASKD_AUTOSTART=1",
		SetupHint:              "`ccb opencode`",
	}))
}
