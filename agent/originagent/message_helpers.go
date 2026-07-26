package originagent

import (
	"strings"

	"github.com/cloudwego/eino/schema"
)

func messageTokens(msg *schema.AgenticMessage) (int, int, int) {
	if msg == nil || msg.ResponseMeta == nil || msg.ResponseMeta.TokenUsage == nil {
		return 0, 0, 0
	}
	usage := msg.ResponseMeta.TokenUsage
	return usage.PromptTokens, usage.CompletionTokens, usage.PromptTokenDetails.CachedTokens
}

func assistantText(msg *schema.AgenticMessage) string {
	if msg == nil {
		return ""
	}

	var parts []string
	for _, block := range msg.ContentBlocks {
		if block == nil || block.AssistantGenText == nil {
			continue
		}
		parts = append(parts, block.AssistantGenText.Text)
	}
	return strings.Join(parts, "")
}

func messageReasoning(msg *schema.AgenticMessage) string {
	if msg == nil {
		return ""
	}

	var parts []string
	for _, block := range msg.ContentBlocks {
		if block == nil || block.Reasoning == nil {
			continue
		}
		parts = append(parts, block.Reasoning.Text)
	}
	return strings.Join(parts, "\n")
}

func messagePlainText(msg *schema.AgenticMessage) string {
	if msg == nil {
		return ""
	}

	var parts []string
	for _, block := range msg.ContentBlocks {
		if block == nil {
			continue
		}
		switch {
		case block.UserInputText != nil:
			parts = append(parts, block.UserInputText.Text)
		case block.AssistantGenText != nil:
			parts = append(parts, block.AssistantGenText.Text)
		case block.Reasoning != nil:
			parts = append(parts, block.Reasoning.Text)
		case block.FunctionToolCall != nil:
			parts = append(parts, block.FunctionToolCall.Name, block.FunctionToolCall.Arguments)
		case block.FunctionToolResult != nil:
			parts = append(parts, functionToolResultText(block.FunctionToolResult))
		}
	}

	return strings.Join(parts, " ")
}

func functionToolCalls(msg *schema.AgenticMessage) []*schema.FunctionToolCall {
	if msg == nil {
		return nil
	}

	calls := make([]*schema.FunctionToolCall, 0)
	for _, block := range msg.ContentBlocks {
		if block == nil || block.FunctionToolCall == nil {
			continue
		}
		calls = append(calls, block.FunctionToolCall)
	}
	return calls
}

func functionToolResultText(result *schema.FunctionToolResult) string {
	if result == nil {
		return ""
	}

	parts := make([]string, 0, len(result.Content))
	for _, block := range result.Content {
		if block == nil {
			continue
		}
		if block.Type == schema.FunctionToolResultContentBlockTypeText && block.Text != nil {
			parts = append(parts, block.Text.Text)
		}
	}
	return strings.Join(parts, " ")
}

func toolResultContentBlocks(observation string, images []*schema.ContentBlock) []*schema.FunctionToolResultContentBlock {
	blocks := []*schema.FunctionToolResultContentBlock{
		{
			Type: schema.FunctionToolResultContentBlockTypeText,
			Text: &schema.UserInputText{Text: observation},
		},
	}

	for _, image := range images {
		if image == nil || image.UserInputImage == nil {
			continue
		}
		blocks = append(blocks, &schema.FunctionToolResultContentBlock{
			Type:  schema.FunctionToolResultContentBlockTypeImage,
			Image: image.UserInputImage,
		})
	}

	return blocks
}
