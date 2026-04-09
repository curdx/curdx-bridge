// bask - Send message to CodeBuddy and wait for reply (sync).
// Source: claude_code_bridge/bin/bask
package main

import (
	"os"

	"github.com/anthropics/curdx-bridge/internal/askcli"
	"github.com/anthropics/curdx-bridge/internal/providers"
)

func main() {
	os.Exit(askcli.Run(askcli.ProviderCLIConfig{
		CmdName:      "bask",
		ProviderName: "CodeBuddy",
		ProviderKey:  "codebuddy",
		Spec:         providers.BaskClientSpec,
		AsyncGuardrail: `[CCB_ASYNC_SUBMITTED provider=codebuddy]
IMPORTANT: Task submitted to CodeBuddy. You MUST:
1. Tell user "CodeBuddy processing..."
2. END YOUR TURN IMMEDIATELY
3. Do NOT wait, poll, check status, or use any more tools
`,
		DefaultTimeout:         3600.0,
		HasRetryLoop:           true,
		StartupWaitEnv:         "CCB_BASKD_STARTUP_WAIT_S",
		RetryWaitEnv:           "CCB_BASKD_RETRY_WAIT_S",
		DaemonHint:             "askd",
		DaemonAutostartEnvHint: "CCB_BASKD_AUTOSTART=1",
		SetupHint:              "`ccb codebuddy`",
	}))
}
