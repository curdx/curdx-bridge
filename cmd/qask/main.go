// qask - Send message to Qwen and wait for reply (sync).
// Source: claude_code_bridge/bin/qask
package main

import (
	"os"

	"github.com/anthropics/curdx-bridge/internal/askcli"
	"github.com/anthropics/curdx-bridge/internal/providers"
)

func main() {
	os.Exit(askcli.Run(askcli.ProviderCLIConfig{
		CmdName:      "qask",
		ProviderName: "Qwen",
		ProviderKey:  "qwen",
		Spec:         providers.QaskClientSpec,
		AsyncGuardrail: `[CCB_ASYNC_SUBMITTED provider=qwen]
IMPORTANT: Task submitted to Qwen. You MUST:
1. Tell user "Qwen processing..."
2. END YOUR TURN IMMEDIATELY
3. Do NOT wait, poll, check status, or use any more tools
`,
		DefaultTimeout:         3600.0,
		HasRetryLoop:           true,
		StartupWaitEnv:         "CCB_QASKD_STARTUP_WAIT_S",
		RetryWaitEnv:           "CCB_QASKD_RETRY_WAIT_S",
		DaemonHint:             "qaskd",
		DaemonAutostartEnvHint: "CCB_QASKD_AUTOSTART=1",
		SetupHint:              "`ccb qwen`",
	}))
}
