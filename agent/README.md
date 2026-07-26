# Agent SDK

`agent` 是 goat 的 Go Agent SDK。它基于 CloudWeGo Eino 的 `model.AgenticModel`，提供原生模型工具调用、对话记忆、上下文压缩、任务规划、Skills、MCP、工具插件、多模态输入和结果流式回调等能力。

当前 Agent 实现位于 `originagent`。模型负责决定是否以及如何调用工具，SDK 负责工具执行、消息持久化、上下文管理和最终结果输出。

## 功能概览

- 原生 Function Calling，可在一轮模型响应中执行多个工具调用。
- 支持 OpenAI、Claude、Gemini 等实现了 Eino `model.AgenticModel` 的模型。
- 支持文件、内存、SQLite 和 MySQL 四种对话记忆后端。
- 支持使用 `MemoryUID` 继续已有会话。
- 支持精确压缩、激进压缩和选择性丢弃旧工具过程三种上下文压缩策略。
- 支持任务计划创建、更新以及多个工具并行执行。
- 支持从 `skills/` 目录加载 Skill。
- 支持 MCP 工具、Go Shared Library 插件和 gRPC 工具插件。
- 支持文本、图片 URL、Base64 图片和二进制图片输入。
- `Do` 直接返回本轮执行步骤流，步骤可携带 Token Usage，并支持最终答案流式回调、执行回调和最终答案 Webhook。

## 目录结构

```text
agent/
├── common/                  # 公共接口、消息、工具、记忆、上下文和配置类型
│   ├── agent.go             # Agent、AgentDoArgs、回调和压缩配置
│   ├── agentic_message.go   # 文本与图片消息构造函数
│   ├── ctx.go               # 带线程安全元数据的 AgentContext
│   ├── memory.go            # Memory 接口和 MemoryUID
│   ├── mcp_tool.go          # MCP Tool 到 common.Tool 的适配
│   ├── step.go              # Agent 执行步骤结构
│   └── tool.go              # Tool、ToolResult 和 JSON Schema 辅助函数
├── memory/
│   ├── filemem/             # 文件存储，默认目录 data/conversations
│   ├── mysql/               # MySQL 存储
│   ├── ram/                 # 进程内存储
│   └── sqlite/              # SQLite 存储，默认文件 data/goat_memory.sqlite
├── originagent/             # 基于原生 Function Calling 的 Agent 实现
│   └── compression/         # 独立上下文压缩包，各策略分别实现
│       ├── precise.go       # 结构化检查点策略
│       ├── aggressive.go    # 文本摘要策略
│       └── discard_half.go  # 选择性丢弃策略
├── toolplugin/              # Shared Library 与 gRPC 工具插件
└── tools/                   # 内置 Planning、Skills 和 Terminal 工具
```

## 安装

项目要求 Go 1.25.8 或更高版本。

```bash
go get github.com/torrischen/goat
```

如果通过私有仓库拉取，请先按仓库根目录 README 配置 `GOPRIVATE` 和 `GOINSECURE`。

## 快速开始

下面以 OpenAI Responses API 为例。`modelMaxTokensK` 的单位是 **千 Token**，例如 `128` 表示模型上下文上限约为 128K Token。

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/memory/ram"
	"github.com/torrischen/goat/agent/originagent"
	"github.com/torrischen/goat/streaming"
	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
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

	agent := originagent.NewAgent(llm, 128, ram.NewRAMMemory())

	memoryUID, stepStream, err := agent.Do(ctx, &common.AgentDoArgs{
		UserInput: common.AgentUserInput{Text: "用三句话介绍 goat Agent SDK"},
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

	fmt.Printf("\nMemoryUID: %s\n", memoryUID)
	fmt.Printf("Token usage: prompt=%d cached=%d completion=%d\n",
		promptTokens, cachedTokens, completionTokens)
}
```

`Do` 会写入本轮用户消息、启动后台 Agent loop，然后立即返回本轮的 `MemoryUID` 和 `Step` 流。每次 `Do` 都有独立的流，不再通过轮询 Memory 推断本轮边界；Agent 正常完成、被中断、Context 取消或后台执行出错时流都会关闭。工具步骤在执行及回调结束后写入流，最终答案步骤在落库后写入流。`Step.ModelUsage` 是 SDK 内部模型调用用量，`Step.CallbackUsage` 可用于记录回调自身产生的用量，`Step.Usage` 是两者总和。

## 注册自定义工具

使用 `common.NewDefaultTool` 可以快速创建工具。参数必须是 JSON Schema 对象，建议通过 `common.NewToolParameters` 构造。

```go
calculator := common.NewDefaultTool(
	"calculator",
	"计算两个数字之和",
	common.NewToolParameters(
		common.ToolProperty{
			Name:        "a",
			Type:        "number",
			Required:    true,
			Description: "第一个数字",
		},
		common.ToolProperty{
			Name:        "b",
			Type:        "number",
			Required:    true,
			Description: "第二个数字",
		},
	),
	func(ctx *common.AgentContext, inputs map[string]any) common.ToolResult {
		a := inputs["a"].(float64)
		b := inputs["b"].(float64)
		return common.NewDefaultToolResult(fmt.Sprintf("%g", a+b))
	},
)

agent.AddTool(context.Background(), calculator)
```

数组和嵌套对象通过 `ToolProperty.Items` 与 `ToolProperty.Properties` 描述，更多示例见 `common/ARRAY_PARAMETERS.md`。

工具名会被自动转换为模型兼容格式；如果名称重复，SDK 会自动追加数字后缀。工具实现可以通过 `AgentContext.GetMeta` 读取本轮调用传入的上下文元数据。

如果某个工具执行后需要暂停后台 Agent loop，例如等待人工确认，可以用 `common.InterruptLoopAfter` 包装普通工具：

```go
agent.AddTool(ctx, common.InterruptLoopAfter(approvalTool))
```

被包装工具仍会正常执行并写入工具结果；SDK 会在当前工具批次落库后停止后台 loop，不会把该暂停作为 `Do` 的错误返回。

## 执行参数

`common.AgentDoArgs` 的主要字段如下：

| 字段 | 说明 |
| --- | --- |
| `UserInput` | 本轮文本和图片输入。 |
| `MemoryUID` | 为空时创建新会话；传入已有 ID 时继续会话。 |
| `MaxStep` | 最大执行轮数，非正数时默认为 `8`。一个批量工具调用只计为一步。 |
| `SpecialRequirements` | 追加到系统提示词，并用于最终答案生成。 |
| `Compress` | 上下文接近模型上限时是否进行压缩。 |
| `CompressionOptions` | 压缩策略及保留的近期消息数。 |
| `ContextMeta` | 注入本轮 `AgentContext` 的线程安全元数据。 |
| `Callbacks` | 工具或最终答案步骤执行前后的回调。 |
| `FinalAnswerStreamingFunc` | 接收最终答案的流式字节片段。 |
| `FinalAnswerWebhook` | 最终答案落库后发送 HTTP Webhook。 |
| `EnablePlanning` | 向模型开放内置计划创建和更新工具。 |
| `PlanUsageInstruction` | 启用 Planning 后，指导模型何时以及如何制定计划。 |
| `ToolExecutionOptions` | 配置多个工具调用是否并行以及最大并发数。 |
| `SkillUsageInstruction` | 指导模型何时以及如何使用 Skill。 |

### 上下文压缩

```go
Compress: true,
CompressionOptions: common.CompressionOptions{
	Strategy:       common.CompressionStrategyPrecise,
	RecentMessages: 12,
},
```

三种策略都会原样保留 system、所有用户输入、Agent 最终回答，以及 `load_skills` / `read_specified_file_in_skill` 的调用与结果；压缩只作用于详细工具过程。普通工具被重复调用时，同一压缩区间内相同工具的结果会先归并到一条消息中，同时保留每次调用的 `CallID`、原始内容块和先后顺序；上述受保护消息不参与归并。`RecentMessages` 会额外保留最近若干条原始消息。

可用策略：

- `CompressionStrategyPrecise`：将较早的详细工具过程转换为结构化检查点，优先保留精确引用。
- `CompressionStrategyAggressive`：将较早的详细工具过程总结为文本，同时保留近期原始消息。
- `CompressionStrategyDiscardHalf`：不调用模型，只丢弃最旧的一半详细工具过程。

## 对话记忆

所有记忆实现均满足 `common.Memory` 接口：

```go
type Memory interface {
	InitNew(context.Context) MemoryUID
	NewMemoryUID(context.Context) MemoryUID
	Append(context.Context, MemoryUID, *schema.AgenticMessage) error
	GetAll(context.Context, MemoryUID) []*schema.AgenticMessage
	Len(context.Context, MemoryUID) int
	Reset(context.Context, MemoryUID, []*schema.AgenticMessage)
	Delete(context.Context, MemoryUID) error
}
```

### 后端选择

```go
// 进程内存储，适合测试。
mem := ram.NewRAMMemory()

// 文件存储；空路径使用 data/conversations。
mem := filemem.NewFileMemory("")

// SQLite；空路径使用 data/goat_memory.sqlite。
mem, err := sqlite.NewSQLiteMemory("")

// MySQL；构造时会自动迁移所需表结构。
mem, err := mysql.NewMySQLMemory("127.0.0.1", 3306, "user", "password", "goat")
```

`originagent.NewAgent(llm, modelMaxTokensK, nil)` 默认使用文件存储。

### 继续会话

```go
memoryUID, firstRun, err := agent.Do(ctx, &common.AgentDoArgs{
	UserInput: common.AgentUserInput{Text: "记住项目代号是 goat"},
})
// 读取 firstRun，直到返回 streaming.ErrStreamClosed。

_, secondRun, err := agent.Do(ctx, &common.AgentDoArgs{
	MemoryUID: memoryUID,
	UserInput: common.AgentUserInput{Text: "项目代号是什么？"},
})
// 读取 secondRun，直到返回 streaming.ErrStreamClosed。
```

继续会话时，SDK 会加载历史消息并使用本轮参数更新系统提示词。由于 `Do` 是异步启动后台 loop，应先读取上一轮返回的 Step 流直到关闭，确认该轮已经结束或暂停，再使用同一个 `MemoryUID` 发起下一轮。

## 多模态输入

```go
input := common.AgentUserInput{
	Text: "描述图片中的内容",
	Images: []*schema.ContentBlock{
		common.ImageURLWithDetailBlock("https://example.com/image.png", "high"),
		common.BinaryImageBlock("image/png", imageBytes),
	},
}
```

可用辅助函数包括：

- `ImageURLBlock` / `ImageURLWithDetailBlock`
- `BinaryImageBlock`
- `Base64ImageBlock`
- `TextBlock` / `AssistantTextBlock` / `ReasoningBlock`

具体模型是否支持图片及 `detail` 参数，由使用的 `model.AgenticModel` 实现决定。

## Planning 与并行工具

`NewAgent` 会注册内置 `generate_plan` 和 `update_plan` 工具，但只有启用 Planning 时才向模型公开。

```go
_, stepStream, err := agent.Do(ctx, &common.AgentDoArgs{
	UserInput:      common.AgentUserInput{Text: "分析项目并完成重构"},
	EnablePlanning: true,
	PlanUsageInstruction: "复杂任务先创建计划，每完成一步后更新状态。",
	ToolExecutionOptions: &common.ToolExecutionOptions{
		EnableParallel: true,
		MaxConcurrency: 4,
	},
})
```

未设置 `MaxConcurrency` 时，并行模式默认最大并发数为 `3`；未启用并行时工具按顺序执行。

## Skills

Skill 默认从当前工作目录的 `skills/` 加载。每个 Skill 是一个包含 `SKILL.md` 的子目录：

```text
skills/
└── code-review/
    ├── SKILL.md
    └── references/
```

`SKILL.md` 需要包含由 `---` 包围的头部描述。Agent 启动后调用：

```go
agent.AddSkills(ctx)

// 排除指定 Skill 目录。
agent.AddSkills(ctx, "experimental-skill")
```

加载后会注册 `load_skills` 和 `read_specified_file_in_skill` 工具，使模型可以按需读取 Skill 文件，而不是一次性把全部内容放入上下文。

## MCP 与工具插件

### MCP

```go
err := agent.RegisterMCPTools(ctx, mcpClient)
```

也可以直接调用 `common.ListMCPTools(ctx, mcpClient)` 获取 `[]common.Tool`。MCP 的文本、资源及结构化结果会被转换为 Agent 工具结果。

### 插件

```go
// 加载目录中的 Go .so 插件。
err := agent.LoadSharedLibPluginTools(ctx, "./plugins")

// 连接一个或多个 gRPC 工具插件服务。
err := agent.LoadRPCPluginTools(ctx, "127.0.0.1:50051")
```

插件接口、编译方式和 gRPC 服务示例见 `toolplugin/README.md`。

## 回调、流与 Webhook

### 执行回调

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

`AfterToolExecution` 可以修改 `step.Observation`，也可以设置 `step.OptimizationAdvice`，将优化建议注入下一轮模型上下文。两个回调同样会接收到最终答案步骤，因此应通过 `step.IsFinalAnswer` 判断步骤类型。若回调自身也调用模型并产生 Token Usage，使用 `step.AddCallbackUsage(promptTokens, cachedTokens, completionTokens)` 合并，避免覆盖 SDK 已写入的模型用量。

### 读取 Step 流

`Do` 直接返回当前运行的 `common.Step` 流，无需轮询 Memory。工具调用步骤包含回调处理后的输入、Observation、图片和 Usage；最终答案也是一个 `IsFinalAnswer == true` 的步骤。

```go
memoryUID, stepStream, err := agent.Do(ctx, args)
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
	fmt.Printf("memory=%s step=%+v\n", memoryUID, step)
}
```

### 最终答案 Webhook

```go
FinalAnswerWebhook: &common.FinalAnswerWebhookConfig{
	URL: "https://example.com/webhooks/final-answer",
	Headers: map[string]string{
		"Authorization": "Bearer <token>",
	},
	Timeout: 5 * time.Second,
},
```

Webhook Payload 包含事件名、Agent 名称、`MemoryUID`、用户输入、最终答案和生成时间。执行步骤应从 `Do` 返回的流中消费，Payload 的 `steps` 字段当前为空。

## 内置工具

`agent/tools` 提供以下工具构造函数：

- `GeneratePlan()` / `UpdatePlan()`：维护当前任务计划。
- `LoadSkills()` / `ReadSpecifiedFileInSkill()`：发现和读取 Skill。
- `Terminal()`：执行参数化命令。
- `ShellCommand()`：通过 shell 执行命令字符串。

Terminal 工具会限制执行超时和输出大小。它具有直接执行本机命令的能力，只应在受控环境中按需注册，并配合目录、权限和容器隔离策略使用。

## 测试

运行 Agent 模块测试：

```bash
go test ./agent/...
```

运行重点子模块测试：

```bash
go test ./agent/originagent/... ./agent/tools ./agent/memory/sqlite ./agent/toolplugin
```

## 使用建议

- `modelMaxTokensK` 应与实际模型上下文长度一致，否则压缩时机可能不准确。
- 生产环境优先使用 SQLite 或 MySQL；RAM Memory 仅适合测试和短生命周期进程。
- 工具参数应做好类型检查，不要直接信任模型生成的参数。
- 对有副作用的工具增加鉴权、幂等、超时和审计机制。
- 需要统计 Token 时，可在 `Do` 返回的 Step 流或执行回调中读取 `Step.Usage`；一轮模型响应触发多个工具调用时，模型用量只会挂在该批第一个工具步骤上，避免聚合时重复计数。
- 使用 `context.WithTimeout` 或 `context.WithCancel` 控制整轮 Agent 生命周期。
