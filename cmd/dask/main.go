// dask - Send message to Droid and wait for reply (sync).
// Source: claude_code_bridge/bin/dask
package main

import (
	"os"

	"github.com/anthropics/curdx-bridge/internal/askcli"
	"github.com/anthropics/curdx-bridge/internal/providers"
)

func main() {
	os.Exit(askcli.Run(askcli.ProviderCLIConfig{
		CmdName:      "dask",
		ProviderName: "Droid",
		ProviderKey:  "droid",
		Spec:         providers.DaskClientSpec,
		AsyncGuardrail: `[CCB_ASYNC_SUBMITTED provider=droid]
IMPORTANT: Task submitted to Droid. You MUST:
1. Tell user "Droid processing..."
2. END YOUR TURN IMMEDIATELY
3. Do NOT wait, poll, check status, or use any more tools
`,
		DefaultTimeout:         3600.0,
		HasRetryLoop:           true,
		StartupWaitEnv:         "CCB_DASKD_STARTUP_WAIT_S",
		RetryWaitEnv:           "CCB_DASKD_RETRY_WAIT_S",
		DaemonHint:             "daskd",
		DaemonAutostartEnvHint: "CCB_DASKD_AUTOSTART=1",
		SetupHint:              "`ccb droid`",
	}))
}
