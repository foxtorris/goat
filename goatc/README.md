# goatc

`goatc` compiles Go tool plugins, embeds them with a YAML-configured goat agent, and produces one Bubble Tea executable. The executable extracts its embedded plugins to a temporary directory while it is running.

## Tool contract

Each tool source must build as a Go `main` package and export the constructor expected by `agent/toolplugin`:

```go
package main

import (
	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/toolplugin"
)

type Tool struct{}

func (t *Tool) Init() error { return nil }
func (t *Tool) Ping() error { return nil }
func (t *Tool) Name() string { return "echo" }
func (t *Tool) Description() string { return "Echo the supplied text." }
func (t *Tool) Parameters() common.ToolParameters {
	return common.NewToolParameters(common.ToolProperty{
		Name: "text", Type: "string", Required: true,
	})
}
func (t *Tool) Execute(_ *common.AgentContext, input map[string]any) common.ToolResult {
	return common.NewDefaultToolResult(input["text"].(string))
}

func New() toolplugin.ToolPlugin { return &Tool{} }
func main() {}
```

A configured source directory produces one `.so`. Agent and plugin builds use the same local Go toolchain and build tags.

## Configuration

```yaml
version: v1

agent:
  name: ops-agent
  model_max_tokens_k: 128
  max_steps: 12
  enable_planning: true
  parallel_tools: 3
  compress: true
  special_requirements:
    - Keep answers concise.

model:
  provider: openai # openai, claude/anthropic, or gemini
  name: gpt-5
  api_key_env: OPENAI_API_KEY
  # base_url: https://example.com/v1
  # max_output_tokens: 4096

context:
  backend: file # ram, file, or sqlite
  path: data/conversations

tools:
  - name: search
    source: ./tools/search
  - name: shell
    source: ./tools/shell

build:
  output: ./dist/ops-agent
  # tags: [production]

tui:
  welcome: Ask me to investigate an issue.
```

Paths are relative to the configuration file. Plugin builds are native-only because Go shared-library plugins cannot be reliably cross-compiled.

## Commands

```bash
# From a Go module that depends on the same goat version as the tools:
go run github.com/torrischen/goat/goatc validate -f goatc.yaml
go run github.com/torrischen/goat/goatc build -f goatc.yaml

# Override build.output:
go run github.com/torrischen/goat/goatc build -f goatc.yaml -o ./dist/agent

OPENAI_API_KEY=... ./dist/agent
```

During a run:

- `Enter` submits a message. A message submitted while the agent is working is queued with `Agent.Steer`.
- `Esc` or `Ctrl+C` cancels the active run.
- `Ctrl+C` exits when the agent is idle.

The resulting executable is a single delivery artifact, but loading an embedded Go plugin requires writing it to the operating system's temporary directory at runtime.
