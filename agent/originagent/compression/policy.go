package compression

import (
	"strings"

	"github.com/torrischen/goat/agent/tools"

	"github.com/cloudwego/eino/schema"
)

const (
	compressionCheckpointPrefix        = "[Context checkpoint v1]\n"
	aggressiveCompressionSummaryPrefix = "[Previous conversation summary]: "
)

func splitSystemMessage(messages []*schema.AgenticMessage) (*schema.AgenticMessage, []*schema.AgenticMessage) {
	if len(messages) > 0 && messages[0] != nil && messages[0].Role == schema.AgenticRoleTypeSystem {
		return messages[0], messages[1:]
	}
	return nil, messages
}

func partitionCompressionMessages(
	messages []*schema.AgenticMessage,
	recentMessages int,
) (toCompress, toKeep []*schema.AgenticMessage) {
	if recentMessages < 0 {
		recentMessages = 0
	}
	recentStart := len(messages) - recentMessages
	if recentStart < 0 {
		recentStart = 0
	}

	protectedSkillCallIDs := collectProtectedSkillCallIDs(messages)
	toCompress = make([]*schema.AgenticMessage, 0, recentStart)
	toKeep = make([]*schema.AgenticMessage, 0, len(messages))
	for index, message := range messages {
		if index >= recentStart || !isDiscardableDetailedMessage(message, protectedSkillCallIDs) {
			toKeep = append(toKeep, message)
			continue
		}
		toCompress = append(toCompress, message)
	}
	return toCompress, toKeep
}

func collectProtectedSkillCallIDs(messages []*schema.AgenticMessage) map[string]struct{} {
	callIDs := make(map[string]struct{})
	for _, message := range messages {
		if message == nil {
			continue
		}
		for _, block := range message.ContentBlocks {
			if block == nil {
				continue
			}
			if call := block.FunctionToolCall; call != nil && isProtectedSkillTool(call.Name) && call.CallID != "" {
				callIDs[call.CallID] = struct{}{}
			}
			if result := block.FunctionToolResult; result != nil && isProtectedSkillTool(result.Name) && result.CallID != "" {
				callIDs[result.CallID] = struct{}{}
			}
		}
	}
	return callIDs
}

func isDiscardableDetailedMessage(message *schema.AgenticMessage, protectedSkillCallIDs map[string]struct{}) bool {
	if message == nil || message.Role == schema.AgenticRoleTypeSystem {
		return false
	}
	if containsProtectedSkillOperation(message, protectedSkillCallIDs) {
		return false
	}
	if isUserInputMessage(message) || isFinalAnswerMessage(message) {
		return false
	}
	return true
}

func containsProtectedSkillOperation(message *schema.AgenticMessage, protectedSkillCallIDs map[string]struct{}) bool {
	if message == nil {
		return false
	}
	for _, block := range message.ContentBlocks {
		if block == nil {
			continue
		}
		if call := block.FunctionToolCall; call != nil {
			if isProtectedSkillTool(call.Name) {
				return true
			}
			if _, ok := protectedSkillCallIDs[call.CallID]; call.CallID != "" && ok {
				return true
			}
		}
		if result := block.FunctionToolResult; result != nil {
			if isProtectedSkillTool(result.Name) {
				return true
			}
			if _, ok := protectedSkillCallIDs[result.CallID]; result.CallID != "" && ok {
				return true
			}
		}
	}
	return false
}

func isProtectedSkillTool(name string) bool {
	return name == tools.InternalToolLoadSkills || name == tools.InternalToolReadSpecifiedFileInSkill
}

func isUserInputMessage(message *schema.AgenticMessage) bool {
	if message == nil || message.Role != schema.AgenticRoleTypeUser {
		return false
	}
	// Tool results also use the user role in AgenticMessage, so role alone is
	// insufficient to identify an actual user input.
	for _, block := range message.ContentBlocks {
		if block != nil && block.FunctionToolResult != nil {
			return false
		}
	}
	return true
}

func isFinalAnswerMessage(message *schema.AgenticMessage) bool {
	if message == nil || message.Role != schema.AgenticRoleTypeAssistant {
		return false
	}
	if isCompressionArtifactMessage(message) {
		return false
	}
	// In originagent, intermediate assistant messages contain tool calls. An
	// assistant message without a tool call is the answer returned to the user.
	for _, block := range message.ContentBlocks {
		if block != nil && block.FunctionToolCall != nil {
			return false
		}
	}
	return true
}

func isCompressionArtifactMessage(message *schema.AgenticMessage) bool {
	text := assistantText(message)
	return strings.HasPrefix(text, compressionCheckpointPrefix) ||
		strings.HasPrefix(text, aggressiveCompressionSummaryPrefix)
}
