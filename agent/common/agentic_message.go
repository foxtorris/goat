package common

import (
	"encoding/base64"

	"github.com/cloudwego/eino/schema"
)

func TextBlock(text string) *schema.ContentBlock {
	return schema.NewContentBlock(&schema.UserInputText{Text: text})
}

func AssistantTextBlock(text string) *schema.ContentBlock {
	return schema.NewContentBlock(&schema.AssistantGenText{Text: text})
}

func ReasoningBlock(text string) *schema.ContentBlock {
	return schema.NewContentBlock(&schema.Reasoning{Text: text})
}

func ImageURLBlock(url string) *schema.ContentBlock {
	return ImageURLWithDetailBlock(url, "")
}

func ImageURLWithDetailBlock(url, detail string) *schema.ContentBlock {
	return schema.NewContentBlock(&schema.UserInputImage{
		URL:    url,
		Detail: schema.ImageURLDetail(detail),
	})
}

func BinaryImageBlock(mimeType string, data []byte) *schema.ContentBlock {
	return schema.NewContentBlock(&schema.UserInputImage{
		Base64Data: base64.StdEncoding.EncodeToString(data),
		MIMEType:   mimeType,
	})
}

func Base64ImageBlock(mimeType, base64Data string) *schema.ContentBlock {
	return schema.NewContentBlock(&schema.UserInputImage{
		Base64Data: base64Data,
		MIMEType:   mimeType,
	})
}

func TextMessage(role schema.AgenticRoleType, text string) *schema.AgenticMessage {
	switch role {
	case schema.AgenticRoleTypeAssistant:
		return AssistantTextMessage(text)
	case schema.AgenticRoleTypeSystem:
		return schema.SystemAgenticMessage(text)
	default:
		return schema.UserAgenticMessage(text)
	}
}

func AssistantTextMessage(text string) *schema.AgenticMessage {
	return &schema.AgenticMessage{
		Role:          schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{AssistantTextBlock(text)},
	}
}

func FunctionToolResultMessage(result *schema.FunctionToolResult) *schema.AgenticMessage {
	return &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(result),
		},
	}
}

func CloneAgenticMessages(messages []*schema.AgenticMessage) []*schema.AgenticMessage {
	if len(messages) == 0 {
		return []*schema.AgenticMessage{}
	}
	result := make([]*schema.AgenticMessage, len(messages))
	copy(result, messages)
	return result
}
