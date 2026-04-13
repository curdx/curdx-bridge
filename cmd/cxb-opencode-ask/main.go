// oask - Send message to OpenCode and wait for reply (sync).
// Source: claude_code_bridge/bin/oask
package main

import (
	"os"

	"github.com/curdx/curdx-bridge/internal/askcli"
	"github.com/curdx/curdx-bridge/internal/providers"
)

func main() {
	os.Exit(askcli.Run(askcli.ProviderCLIConfig{
		CmdName:      "cxb-opencode-ask",
		ProviderName: "OpenCode",
		ProviderKey:  "opencode",
		Spec:         providers.OaskClientSpec,
		AsyncGuardrail: `[CURDX_ASYNC_SUBMITTED provider=opencode]
IMPORTANT: Task submitted to OpenCode. You MUST:
1. Tell user "OpenCode processing..."
2. END YOUR TURN IMMEDIATELY
3. Do NOT wait, poll, check status, or use any more tools
`,
		DefaultTimeout:         3600.0,
		HasRetryLoop:           true,
		HasAsyncMode:           true,
		StartupWaitEnv:         "CURDX_OASKD_STARTUP_WAIT_S",
		RetryWaitEnv:           "CURDX_OASKD_RETRY_WAIT_S",
		DaemonHint:             "cxb-opencode-askd",
		DaemonAutostartEnvHint: "CURDX_OASKD_AUTOSTART=1",
		SetupHint:              "`curdx opencode`",
	}))
}
