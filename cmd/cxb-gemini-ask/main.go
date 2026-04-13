// gask - Send message to Gemini and wait for reply (sync).
// Source: claude_code_bridge/bin/gask
package main

import (
	"os"

	"github.com/curdx/curdx-bridge/internal/askcli"
	"github.com/curdx/curdx-bridge/internal/providers"
)

func main() {
	os.Exit(askcli.Run(askcli.ProviderCLIConfig{
		CmdName:      "cxb-gemini-ask",
		ProviderName: "Gemini",
		ProviderKey:  "gemini",
		Spec:         providers.GaskClientSpec,
		AsyncGuardrail: `[CURDX_ASYNC_SUBMITTED provider=gemini]
IMPORTANT: Task submitted to Gemini. You MUST:
1. Tell user "Gemini processing..."
2. END YOUR TURN IMMEDIATELY
3. Do NOT wait, poll, check status, or use any more tools
`,
		DefaultTimeout:         3600.0,
		HasRetryLoop:           true,
		StartupWaitEnv:         "CURDX_GASKD_STARTUP_WAIT_S",
		RetryWaitEnv:           "CURDX_GASKD_RETRY_WAIT_S",
		DaemonHint:             "cxb-gemini-askd",
		DaemonAutostartEnvHint: "CURDX_GASKD_AUTOSTART=1",
		SetupHint:              "`curdx gemini`",
	}))
}
