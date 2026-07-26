package compression

import (
	"strings"

	"github.com/cloudwego/eino/schema"
)

func messageTokens(message *schema.AgenticMessage) (int, int, int) {
	if message == nil || message.ResponseMeta == nil || message.ResponseMeta.TokenUsage == nil {
		return 0, 0, 0
	}
	usage := message.ResponseMeta.TokenUsage
	return usage.PromptTokens, usage.CompletionTokens, usage.PromptTokenDetails.CachedTokens
}

func assistantText(message *schema.AgenticMessage) string {
	if message == nil {
		return ""
	}

	var parts []string
	for _, block := range message.ContentBlocks {
		if block == nil || block.AssistantGenText == nil {
			continue
		}
		parts = append(parts, block.AssistantGenText.Text)
	}
	return strings.Join(parts, "")
}

func messagePlainText(message *schema.AgenticMessage) string {
	if message == nil {
		return ""
	}

	var parts []string
	for _, block := range message.ContentBlocks {
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
