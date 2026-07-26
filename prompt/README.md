# Prompt Builder

`prompt` 提供一个轻量的链式 API，用于把 Role、Objective、Context、Instructions、Constraints、Input、Output Format 和 Examples 组织成顺序稳定的 Markdown prompt。

## 快速开始

```go
package main

import (
	"fmt"

	"github.com/torrischen/goat/prompt"
)

func main() {
	p := prompt.New().
		Role("你是一名资深 Go 工程师").
		Objective("审查输入代码并指出高风险问题").
		Context("代码运行在支付服务的请求链路中").
		Instructions(
			"优先检查正确性和并发安全",
			"每个问题都给出可执行的修复建议",
		).
		Constraints("不要改变公开 API", "使用中文回答").
		Input("func handle() {}").
		OutputFormat("使用 Markdown checklist").
		Build()

	fmt.Println(p)
}
```

输出：

```markdown
## Role
你是一名资深 Go 工程师

## Objective
审查输入代码并指出高风险问题

## Context
代码运行在支付服务的请求链路中

## Instructions
- 优先检查正确性和并发安全
- 每个问题都给出可执行的修复建议

## Constraints
- 不要改变公开 API
- 使用中文回答

## Input
func handle() {}

## Output Format
使用 Markdown checklist
```

`Role`、`Objective`、`Context`、`Input` 和 `OutputFormat` 再次调用时会覆盖旧值；`Instruction(s)`、`Constraint(s)` 和 `Example` 会按调用顺序追加。空白内容会被忽略。

可以通过 `Section(title, content)` 和 `ListSection(title, items...)` 追加自定义段落；自定义段落会按调用顺序放在标准段落之后。`String()` 与 `Build()` 等价。
