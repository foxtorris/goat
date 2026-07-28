# Agent SDK

`agent` is goat's Go agent SDK. Built on CloudWeGo Eino's `model.AgenticModel`, it provides native model tool calling, conversation context management, context compression, task planning, skills, MCP integration, tool plugins, multimodal input, and streaming result callbacks.

The current agent implementation lives in `react`. The model decides whether and how to call tools; the SDK executes those tools, persists messages, manages context, and produces the final answer.

## Features

- Native function calling with support for multiple tool calls in one model response.
- Compatibility with OpenAI, Claude, Gemini, and any other model that implements Eino's `model.AgenticModel`.
- File, in-memory, SQLite, and MySQL conversation context manager backends.
- Conversation continuation and persistent, protocol-safe steering through `ContextUID`.
- Precise, aggressive, and selective-discard context compression strategies.
- Task plan creation and updates, plus parallel execution of multiple tools.
- Skill loading from a `skills/` directory.
- MCP tools, Go shared-library plugins, and gRPC tool plugins.
- Text, image URL, Base64 image, and binary image input.
- A per-run step stream returned directly by `Do`, with token usage, execution callbacks, final-answer streaming, and final-answer webhooks.

## Directory structure

```text
agent/
├── common/                  # Shared agent, message, tool, context, and configuration types
│   ├── agent.go             # Agent, AgentDoArgs, callbacks, and compression configuration
│   ├── agentic_message.go   # Text and image message constructors
│   ├── ctx.go               # AgentContext with concurrency-safe metadata
│   ├── context_uid.go       # ContextUID conversation identifier
│   ├── mcp_tool.go          # MCP Tool to common.Tool adapter
│   ├── step.go              # Agent execution step type
│   └── tool.go              # Tool, ToolResult, and JSON Schema helpers
├── contextmgr/
│   ├── context_manager.go   # ContextManager interface
│   ├── file/                # File storage; defaults to data/conversations
│   ├── mysql/               # MySQL storage
│   ├── ram/                 # In-process storage
│   └── sqlite/              # SQLite storage; defaults to data/goat_context.sqlite
├── react/                   # Native function-calling agent implementation
│   └── compression/         # Independent context-compression strategies
│       ├── precise.go       # Structured checkpoint strategy
│       ├── aggressive.go    # Text summarization strategy
│       └── discard_half.go  # Selective discard strategy
├── toolplugin/              # Shared-library and gRPC tool plugins
└── tools/                   # Built-in planning, skills, terminal, and shell tools
```

## Installation

The project requires Go 1.25.8 or newer.

```bash
go get github.com/torrischen/goat/agent/react
go get github.com/torrischen/goat/agent/contextmgr/ram
```

Install the Eino adapter for the model provider you plan to use. For example:

```bash
go get github.com/cloudwego/eino-ext/components/model/agenticopenai
```

## Quick start

The following example uses the OpenAI Responses API. `modelMaxTokensK` is measured in **thousands of tokens**; for example, `128` represents a model context limit of approximately 128K tokens.

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/contextmgr/ram"
	"github.com/torrischen/goat/agent/react"
	"github.com/torrischen/goat/streaming"
)

func main() {
	ctx := context.Background()

	llm, err := agenticopenai.NewResponsesModel(ctx, &agenticopenai.ResponsesConfig{
		APIKey: os.Getenv("OPENAI_API_KEY"),
		Model:  "gpt-5.2",
	})
	if err != nil {
		log.Fatal(err)
	}

	agent := react.NewAgent(llm, 128, ram.NewRAMContextManager())

	contextUID, stepStream, err := agent.Do(ctx, &common.AgentDoArgs{
		UserInput: common.AgentUserInput{Text: "Introduce the goat Agent SDK in three sentences."},
		MaxStep:   8,
		FinalAnswerStreamingFunc: func(_ context.Context, chunk []byte) error {
			fmt.Print(string(chunk))
			return nil
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	var promptTokens, cachedTokens, completionTokens int
	for {
		step, err := stepStream.ReadWithContext(ctx)
		if errors.Is(err, streaming.ErrStreamClosed) {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		if step.Usage != nil {
			promptTokens += step.Usage.PromptTokens
			cachedTokens += step.Usage.CachedTokens
			completionTokens += step.Usage.CompletionTokens
		}
	}

	fmt.Printf("\nContextUID: %s\n", contextUID)
	fmt.Printf("Token usage: prompt=%d cached=%d completion=%d\n",
		promptTokens, cachedTokens, completionTokens)
}
```

`Do` stores the current user message, starts the agent loop in the background, and immediately returns the run's `ContextUID` and `Step` stream. Every call to `Do` has an independent stream, so callers do not need to poll the context manager to infer run boundaries. The stream closes when the agent finishes normally, is interrupted, the context is canceled, or background execution fails. Tool steps enter the stream after execution and callbacks complete; the final-answer step enters the stream after it has been persisted. `Step.ModelUsage` records SDK model calls, `Step.CallbackUsage` can record usage produced by callbacks, and `Step.Usage` is their total.

## Steering a running conversation

`Steer` queues one or more independent user messages in the conversation's context-manager-backed inbox:

```go
err = agent.Steer(ctx, &common.AgentSteerArgs{
	ContextUID: contextUID,
	UserInputs: []common.AgentUserInput{
		{Text: "Do not deploy yet."},
		{Text: "Run the complete test suite first."},
	},
})
```

The current assistant turn is allowed to settle. If it contains tool calls, all corresponding tool results are committed before the queued messages. At that protocol-safe boundary, the context manager atomically appends the completed tool turn followed by the queued user messages, and the next `Think` sees them.

A final answer always wins: it is streamed and committed immediately, while any messages still pending at that boundary are discarded. Once a final assistant message is committed, `Steer` returns `contextmgr.ErrConversationFinalized`. Calling `Do` again appends a new user message and reopens steering for that run. Because final generation and `Steer` can race, a `Steer` accepted just before final commit may still be discarded. Concurrent `Do` calls for the same `ContextUID` remain unsupported.

## Registering custom tools

Use `common.NewDefaultTool` to define a tool quickly. Parameters must be a JSON Schema object; `common.NewToolParameters` is the recommended constructor.

```go
calculator := common.NewDefaultTool(
	"calculator",
	"Add two numbers.",
	common.NewToolParameters(
		common.ToolProperty{
			Name:        "a",
			Type:        "number",
			Required:    true,
			Description: "First number.",
		},
		common.ToolProperty{
			Name:        "b",
			Type:        "number",
			Required:    true,
			Description: "Second number.",
		},
	),
	func(_ *common.AgentContext, inputs map[string]any) common.ToolResult {
		a, aOK := inputs["a"].(float64)
		b, bOK := inputs["b"].(float64)
		if !aOK || !bOK {
			return common.NewDefaultToolResult("a and b must be numbers")
		}
		return common.NewDefaultToolResult(fmt.Sprintf("%g", a+b))
	},
)

agent.AddTool(context.Background(), calculator)
```

Describe arrays and nested objects with `ToolProperty.Items` and `ToolProperty.Properties`. See [common/ARRAY_PARAMETERS.md](common/ARRAY_PARAMETERS.md) for additional examples.

Tool names are automatically converted to a model-compatible format. If names collide, the SDK appends a numeric suffix. Tool implementations can read per-run context metadata with `AgentContext.GetMeta`.

To pause the background agent loop after a tool runs—for example, while waiting for human approval—wrap the tool with `common.InterruptLoopAfter`:

```go
agent.AddTool(ctx, common.InterruptLoopAfter(approvalTool))
```

The wrapped tool still executes and its result is persisted. After the current tool batch is stored, the SDK stops the background loop without returning the pause as an error from `Do`.

## Run options

The main fields in `common.AgentDoArgs` are:

| Field | Description |
| --- | --- |
| `UserInput` | Text and image input for the current run. |
| `ContextUID` | Creates a conversation when empty; continues an existing conversation when set. |
| `MaxStep` | Maximum execution rounds. Values at or below zero default to `8`. A batch of tool calls counts as one step. |
| `SpecialRequirements` | Additional requirements appended to the system prompt and used during final-answer generation. |
| `Compress` | Whether to compress context as it approaches the model limit. |
| `CompressionOptions` | Compression strategy and number of recent messages to retain. |
| `ContextMeta` | Concurrency-safe metadata injected into the run's `AgentContext`. |
| `Callbacks` | Hooks that run before and after tool or final-answer steps. |
| `FinalAnswerStreamingFunc` | Receives streamed byte chunks from the final answer. |
| `FinalAnswerWebhook` | Sends an HTTP webhook after the final answer is persisted. |
| `EnablePlanning` | Exposes the built-in plan creation and update tools to the model. |
| `PlanUsageInstruction` | Tells the model when and how to create plans while planning is enabled. |
| `ToolExecutionOptions` | Controls parallel tool execution and maximum concurrency while planning is enabled. |
| `SkillUsageInstruction` | Tells the model when and how to use skills. |

### Context compression

```go
Compress: true,
CompressionOptions: common.CompressionOptions{
	Strategy:       common.CompressionStrategyPrecise,
	RecentMessages: 12,
},
```

All three strategies preserve system messages, every user input, final agent answers, and calls and results for `load_skills` and `read_specified_file_in_skill`. Only detailed tool-process messages are compressed. When a regular tool is called repeatedly, results from the same tool within a compression range are first merged into one message while retaining each call's `CallID`, original content blocks, and order. Protected messages are never included in this merge. `RecentMessages` preserves an additional number of recent messages in their original form.

Available strategies:

- `CompressionStrategyPrecise` converts older detailed tool-process messages into structured checkpoints, prioritizing exact references.
- `CompressionStrategyAggressive` summarizes older detailed tool-process messages as text while preserving recent raw messages.
- `CompressionStrategyDiscardHalf` calls no model and discards the oldest half of detailed tool-process messages.

## Conversation context management

Every backend implements `contextmgr.ContextManager`:

```go
type TurnCommitResult struct {
	AppliedPendingMessages []*schema.AgenticMessage
}

type ContextManager interface {
	InitNew(context.Context) common.ContextUID
	NewContextUID(context.Context) common.ContextUID
	Append(context.Context, common.ContextUID, *schema.AgenticMessage) error
	GetAll(context.Context, common.ContextUID) []*schema.AgenticMessage
	Len(context.Context, common.ContextUID) int
	Reset(context.Context, common.ContextUID, []*schema.AgenticMessage)
	EnqueuePendingMessages(context.Context, common.ContextUID, []*schema.AgenticMessage) error
	CommitTurn(context.Context, common.ContextUID, []*schema.AgenticMessage) (*TurnCommitResult, error)
	CommitFinal(context.Context, common.ContextUID, *schema.AgenticMessage) error
	Delete(context.Context, common.ContextUID) error
}
```

`EnqueuePendingMessages` stores only user-role messages outside committed history. `CommitTurn` atomically appends a complete non-final turn and then moves all currently pending messages behind it, preserving order. `CommitFinal` atomically appends the final assistant answer and discards pending messages; further enqueue attempts return `ErrConversationFinalized` until a new user input is appended. `Reset` replaces committed history during compression but leaves the pending inbox untouched.

### Choosing a backend

```go
// In-process storage for tests and short-lived processes.
manager := ram.NewRAMContextManager()

// File storage; an empty path uses data/conversations.
manager := file.NewFileContextManager("")

// SQLite; an empty path uses data/goat_context.sqlite.
manager, err := sqlite.NewSQLiteContextManager("")

// MySQL; the constructor automatically migrates the required tables.
manager, err := mysql.NewMysqlContextManager("127.0.0.1", 3306, "user", "password", "goat")
```

`react.NewAgent(llm, modelMaxTokensK, nil)` uses `file.FileContextManager` by default.

### Continuing a conversation

```go
contextUID, firstRun, err := agent.Do(ctx, &common.AgentDoArgs{
	UserInput: common.AgentUserInput{Text: "Remember that the project codename is goat."},
})
// Read firstRun until it returns streaming.ErrStreamClosed.

_, secondRun, err := agent.Do(ctx, &common.AgentDoArgs{
	ContextUID: contextUID,
	UserInput: common.AgentUserInput{Text: "What is the project codename?"},
})
// Read secondRun until it returns streaming.ErrStreamClosed.
```

When continuing a conversation, the SDK loads its history and updates the system prompt with the current run options. Appending the new `Do` user input reopens steering after the previous final answer. Because `Do` starts the agent loop asynchronously, drain the previous step stream until it closes before starting another run with the same `ContextUID`. This confirms that the previous run has finished or paused.

## Multimodal input

```go
input := common.AgentUserInput{
	Text: "Describe the contents of this image.",
	Images: []*schema.ContentBlock{
		common.ImageURLWithDetailBlock("https://example.com/image.png", "high"),
		common.BinaryImageBlock("image/png", imageBytes),
	},
}
```

Available helpers include:

- `ImageURLBlock` / `ImageURLWithDetailBlock`
- `BinaryImageBlock`
- `Base64ImageBlock`
- `TextBlock` / `AssistantTextBlock` / `ReasoningBlock`

Image support and support for the `detail` parameter depend on the selected `model.AgenticModel` implementation.

## Planning and parallel tools

`NewAgent` registers the built-in `generate_plan` and `update_plan` tools, but exposes them to the model only when planning is enabled.

```go
_, stepStream, err := agent.Do(ctx, &common.AgentDoArgs{
	UserInput:      common.AgentUserInput{Text: "Analyze the project and complete the refactor."},
	EnablePlanning: true,
	PlanUsageInstruction: "Create a plan for complex tasks and update it after completing each step.",
	ToolExecutionOptions: &common.ToolExecutionOptions{
		EnableParallel: true,
		MaxConcurrency: 4,
	},
})
```

When `MaxConcurrency` is not set, parallel mode defaults to a maximum concurrency of `3`. When parallel mode is disabled, tools execute sequentially.

## Skills

Skills are loaded from `skills/` in the current working directory by default. Each skill is a subdirectory containing a `SKILL.md` file:

```text
skills/
└── code-review/
    ├── SKILL.md
    └── references/
```

`SKILL.md` must contain a header description enclosed by `---` delimiters. After creating the agent, load skills with:

```go
agent.AddSkills(ctx)

// Exclude a specific skill directory.
agent.AddSkills(ctx, "experimental-skill")
```

Loading skills registers the `load_skills` and `read_specified_file_in_skill` tools. The model can then read skill files on demand instead of placing all skill content in context up front.

## MCP and tool plugins

### MCP

```go
err := agent.RegisterMCPTools(ctx, mcpClient)
```

You can also call `common.ListMCPTools(ctx, mcpClient)` directly to obtain `[]common.Tool`. MCP text, resource, and structured results are converted into agent tool results.

### Plugins

```go
// Load Go .so plugins from a directory.
err := agent.LoadSharedLibPluginTools(ctx, "./plugins")

// Connect to one or more gRPC tool-plugin services.
err := agent.LoadRPCPluginTools(ctx, "127.0.0.1:50051")
```

See the [tool plugin cookbook](toolplugin/README.md) for plugin interfaces, build instructions, and a gRPC service example.

## Callbacks, streams, and webhooks

### Execution callbacks

```go
Callbacks: &common.Callbacks{
	BeforeToolExecution: func(ctx *common.AgentContext, step *common.Step) {
		fmt.Printf("before: %s\n", step.ToolName)
	},
	AfterToolExecution: func(ctx *common.AgentContext, step *common.Step) {
		fmt.Printf("after: %s\n", step.Observation)
	},
},
```

`AfterToolExecution` may modify `step.Observation` or set `step.OptimizationAdvice`, which injects guidance into the next model context. Both callbacks also receive final-answer steps, so inspect `step.IsFinalAnswer` to distinguish step types. If a callback invokes a model and produces token usage, merge it with `step.AddCallbackUsage(promptTokens, cachedTokens, completionTokens)` instead of overwriting usage recorded by the SDK.

### Reading the step stream

`Do` returns the current run's `common.Step` stream directly, so context manager polling is unnecessary. Tool-call steps include callback-processed input, observations, images, and usage. The final answer is emitted as a step where `IsFinalAnswer == true`.

```go
contextUID, stepStream, err := agent.Do(ctx, args)
if err != nil {
	return err
}
for {
	step, err := stepStream.ReadWithContext(ctx)
	if errors.Is(err, streaming.ErrStreamClosed) {
		break
	}
	if err != nil {
		return err
	}
	fmt.Printf("conversation=%s step=%+v\n", contextUID, step)
}
```

### Final-answer webhook

```go
FinalAnswerWebhook: &common.FinalAnswerWebhookConfig{
	URL: "https://example.com/webhooks/final-answer",
	Headers: map[string]string{
		"Authorization": "Bearer <token>",
	},
	Timeout: 5 * time.Second,
},
```

The webhook payload contains the event name, agent name, `ContextUID`, user input, final answer, and generation time. Consume execution steps from the stream returned by `Do`; the payload's `steps` field is currently empty.

## Built-in tools

`agent/tools` provides these constructors:

- `GeneratePlan()` / `UpdatePlan()` maintain the current task plan.
- `LoadSkills()` / `ReadSpecifiedFileInSkill()` discover and read skills.
- `Terminal()` executes a parameterized command.
- `ShellCommand()` executes a command string through a shell.

The terminal tool limits execution time and output size. It can execute local commands directly, so register it only when needed in a controlled environment and combine it with working-directory, permission, and container isolation policies.

## Testing

Run all agent tests:

```bash
go test ./agent/...
```

Run the primary submodule tests:

```bash
go test ./agent/react/... ./agent/tools ./agent/contextmgr/sqlite ./agent/toolplugin
```

## Best practices

- Set `modelMaxTokensK` to the model's real context length so compression starts at the correct time.
- Prefer SQLite or MySQL context managers in production. The RAM context manager is intended for tests and short-lived processes.
- Validate tool parameter types; never trust model-generated arguments directly.
- Add authorization, idempotency, timeouts, and audit logging to tools with side effects.
- Read `Step.Usage` from the stream returned by `Do` or from execution callbacks when aggregating tokens. If one model response triggers multiple tool calls, model usage appears only on the first tool step in that batch to prevent double counting.
- Use `context.WithTimeout` or `context.WithCancel` to control the lifecycle of the complete agent run.
