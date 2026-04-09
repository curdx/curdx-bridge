// gask - Send message to Gemini and wait for reply (sync).
// Source: claude_code_bridge/bin/gask
package main

import (
	"os"

	"github.com/anthropics/curdx-bridge/internal/askcli"
	"github.com/anthropics/curdx-bridge/internal/providers"
)

func main() {
	os.Exit(askcli.Run(askcli.ProviderCLIConfig{
		CmdName:      "gask",
		ProviderName: "Gemini",
		ProviderKey:  "gemini",
		Spec:         providers.GaskClientSpec,
		AsyncGuardrail: `[CCB_ASYNC_SUBMITTED provider=gemini]
IMPORTANT: Task submitted to Gemini. You MUST:
1. Tell user "Gemini processing..."
2. END YOUR TURN IMMEDIATELY
3. Do NOT wait, poll, check status, or use any more tools
`,
		DefaultTimeout:         3600.0,
		HasRetryLoop:           true,
		StartupWaitEnv:         "CCB_GASKD_STARTUP_WAIT_S",
		RetryWaitEnv:           "CCB_GASKD_RETRY_WAIT_S",
		DaemonHint:             "gaskd",
		DaemonAutostartEnvHint: "CCB_GASKD_AUTOSTART=1",
		SetupHint:              "`ccb gemini`",
	}))
}
