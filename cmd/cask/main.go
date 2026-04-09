// cask - Send message to Codex and wait for reply (sync).
// Source: claude_code_bridge/bin/cask
package main

import (
	"os"

	"github.com/anthropics/curdx-bridge/internal/askcli"
	"github.com/anthropics/curdx-bridge/internal/providers"
)

func main() {
	os.Exit(askcli.Run(askcli.ProviderCLIConfig{
		CmdName:      "cask",
		ProviderName: "Codex",
		ProviderKey:  "codex",
		Spec:         providers.CaskClientSpec,
		AsyncGuardrail: `[CCB_ASYNC_SUBMITTED provider=codex]
IMPORTANT: Task submitted to Codex. You MUST:
1. Tell user "Codex processing..."
2. END YOUR TURN IMMEDIATELY
3. Do NOT wait, poll, check status, or use any more tools
`,
		DefaultTimeout:         -1.0,
		HasRetryLoop:           false,
		DaemonHint:             "caskd",
		DaemonAutostartEnvHint: "CCB_CASKD_AUTOSTART=1",
		SetupHint:              "`ccb codex`",
		HasSupervisorMode:      true,
	}))
}
