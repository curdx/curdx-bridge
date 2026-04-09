// hask - Send message to Copilot and wait for reply (sync).
// Source: claude_code_bridge/bin/hask
package main

import (
	"os"

	"github.com/anthropics/curdx-bridge/internal/askcli"
	"github.com/anthropics/curdx-bridge/internal/providers"
)

func main() {
	os.Exit(askcli.Run(askcli.ProviderCLIConfig{
		CmdName:      "hask",
		ProviderName: "Copilot",
		ProviderKey:  "copilot",
		Spec:         providers.HaskClientSpec,
		AsyncGuardrail: `[CCB_ASYNC_SUBMITTED provider=copilot]
IMPORTANT: Task submitted to Copilot. You MUST:
1. Tell user "Copilot processing..."
2. END YOUR TURN IMMEDIATELY
3. Do NOT wait, poll, check status, or use any more tools
`,
		DefaultTimeout:         3600.0,
		HasRetryLoop:           true,
		StartupWaitEnv:         "CCB_HASKD_STARTUP_WAIT_S",
		RetryWaitEnv:           "CCB_HASKD_RETRY_WAIT_S",
		DaemonHint:             "askd",
		DaemonAutostartEnvHint: "CCB_HASKD_AUTOSTART=1",
		SetupHint:              "`ccb copilot`",
	}))
}
