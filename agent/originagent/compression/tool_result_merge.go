package compression

import "github.com/cloudwego/eino/schema"

// mergeSameToolResultMessages coalesces result-only messages for the same
// tool into one user message without changing their result payloads. Each
// invocation remains a separate FunctionToolResult block, so its call ID and
// multimodal content are retained.
//
// Groups are emitted at the last result position. This keeps all corresponding
// tool calls before the merged results. A group never crosses a message that is
// protected by the compression policy (system/user/final-answer/skill
// messages), and protected skill results are never merged.
func mergeSameToolResultMessages(messages []*schema.AgenticMessage) []*schema.AgenticMessage {
	return mergeSameToolResultMessagesWithCallNames(messages, collectFunctionToolCallNames(messages))
}

func mergeSameToolResultMessagesWithCallNames(
	messages []*schema.AgenticMessage,
	callNames map[string]string,
) []*schema.AgenticMessage {
	if len(messages) < 2 {
		return messages
	}

	type groupKey struct {
		segment int
		name    string
	}
	type blockCandidate struct {
		key groupKey
		ok  bool
	}

	protectedSkillCallIDs := collectProtectedSkillCallIDs(messages)
	candidates := make([][]blockCandidate, len(messages))
	messageCounts := make(map[groupKey]int)
	lastIndexes := make(map[groupKey]int)
	segment := 0

	for messageIndex, message := range messages {
		if !isDiscardableDetailedMessage(message, protectedSkillCallIDs) {
			// Do not move ordinary tool results across durable conversation
			// boundaries while grouping them.
			segment++
			continue
		}
		if _, ok := functionToolResultsOnly(message); !ok {
			continue
		}

		candidates[messageIndex] = make([]blockCandidate, len(message.ContentBlocks))
		keysInMessage := make(map[groupKey]struct{})
		for blockIndex, block := range message.ContentBlocks {
			if block == nil || block.FunctionToolResult == nil {
				continue
			}
			result := block.FunctionToolResult
			name := resolvedFunctionToolResultName(result, callNames)
			if name == "" || isProtectedSkillTool(name) {
				continue
			}
			if _, protected := protectedSkillCallIDs[result.CallID]; result.CallID != "" && protected {
				continue
			}

			key := groupKey{segment: segment, name: name}
			candidates[messageIndex][blockIndex] = blockCandidate{key: key, ok: true}
			keysInMessage[key] = struct{}{}
		}
		for key := range keysInMessage {
			messageCounts[key]++
			lastIndexes[key] = messageIndex
		}
	}

	hasMerge := false
	for _, count := range messageCounts {
		if count > 1 {
			hasMerge = true
			break
		}
	}
	if !hasMerge {
		return messages
	}

	groupedBlocks := make(map[groupKey][]*schema.ContentBlock, len(messageCounts))
	for messageIndex, descriptors := range candidates {
		for blockIndex, descriptor := range descriptors {
			if !descriptor.ok || messageCounts[descriptor.key] < 2 {
				continue
			}
			groupedBlocks[descriptor.key] = append(
				groupedBlocks[descriptor.key],
				messages[messageIndex].ContentBlocks[blockIndex],
			)
		}
	}

	mergedMessages := make([]*schema.AgenticMessage, 0, len(messages))
	for messageIndex, message := range messages {
		descriptors := candidates[messageIndex]
		if len(descriptors) == 0 {
			mergedMessages = append(mergedMessages, message)
			continue
		}

		changed := false
		emitted := make(map[groupKey]struct{})
		blocks := make([]*schema.ContentBlock, 0, len(message.ContentBlocks))
		for blockIndex, block := range message.ContentBlocks {
			descriptor := descriptors[blockIndex]
			if !descriptor.ok || messageCounts[descriptor.key] < 2 {
				blocks = append(blocks, block)
				continue
			}

			changed = true
			if lastIndexes[descriptor.key] != messageIndex {
				continue
			}
			if _, alreadyEmitted := emitted[descriptor.key]; alreadyEmitted {
				continue
			}
			emitted[descriptor.key] = struct{}{}
			blocks = append(blocks, groupedBlocks[descriptor.key]...)
		}
		if !changed {
			mergedMessages = append(mergedMessages, message)
			continue
		}
		if len(blocks) == 0 {
			continue
		}

		// Clone only the message container. The original content/result blocks
		// are immutable inputs and can safely be retained by pointer.
		merged := *message
		merged.ContentBlocks = blocks
		mergedMessages = append(mergedMessages, &merged)
	}
	return mergedMessages
}

func collectFunctionToolCallNames(messages []*schema.AgenticMessage) map[string]string {
	callNames := make(map[string]string)
	for _, message := range messages {
		if message == nil {
			continue
		}
		for _, block := range message.ContentBlocks {
			if block == nil || block.FunctionToolCall == nil {
				continue
			}
			call := block.FunctionToolCall
			if call.CallID != "" && call.Name != "" {
				callNames[call.CallID] = call.Name
			}
		}
	}
	return callNames
}

func resolvedFunctionToolResultName(
	result *schema.FunctionToolResult,
	callNames map[string]string,
) string {
	if result == nil {
		return ""
	}
	if result.Name != "" {
		return result.Name
	}
	return callNames[result.CallID]
}

// functionToolResultsOnly returns all function-tool results when message has no
// other non-nil content block. This avoids changing mixed user messages.
func functionToolResultsOnly(message *schema.AgenticMessage) ([]*schema.FunctionToolResult, bool) {
	if message == nil || message.Role != schema.AgenticRoleTypeUser {
		return nil, false
	}

	results := make([]*schema.FunctionToolResult, 0, len(message.ContentBlocks))
	for _, block := range message.ContentBlocks {
		if block == nil {
			continue
		}
		if block.FunctionToolResult == nil {
			return nil, false
		}
		results = append(results, block.FunctionToolResult)
	}
	return results, len(results) > 0
}
