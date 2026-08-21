package react

import (
	"errors"
	"fmt"
	"io"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/react/compression"
	"github.com/torrischen/goat/streaming"
	"github.com/torrischen/goat/util/logging"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type thinkArgs struct {
	Compress           bool
	CompressionOptions common.CompressionOptions
	Messages           []*schema.AgenticMessage
}

type thinkResult struct {
	RawResponse        *schema.AgenticMessage
	IsCompressed       bool
	CompressedMessages []*schema.AgenticMessage
	Messages           []*schema.AgenticMessage
	ModelUsage         *common.AgentUsage
	CompressionUsage   *common.AgentUsage
}

func (a *Agent) think(
	ctx *common.AgentContext,
	args *thinkArgs,
	events streaming.Stream[common.AgentEvent],
	opts ...model.Option,
) (*thinkResult, error) {
	result := &thinkResult{Messages: args.Messages}

	if err := events.WriteWithContext(ctx, common.ModelCallStartedEvent{Phase: common.ModelCallPhaseThink}); err != nil {
		return nil, err
	}
	raw, err := a.streamModelResponse(ctx, args.Messages, events, opts...)
	if err != nil {
		if writeErr := events.WriteWithContext(ctx, common.ModelCallFailedEvent{
			Phase: common.ModelCallPhaseThink,
			Error: err.Error(),
		}); writeErr != nil {
			return nil, fmt.Errorf("model call failed: %v; write failure event: %w", err, writeErr)
		}
		return nil, fmt.Errorf("think model call: %w", err)
	}

	promptTokens, completionTokens, cachedTokens := messageTokens(raw)
	result.ModelUsage = common.NewAgentUsage(promptTokens, cachedTokens, completionTokens)
	toolCalls := functionToolCalls(raw)
	if err := events.WriteWithContext(ctx, common.ModelCallCompletedEvent{
		Phase:        common.ModelCallPhaseThink,
		Usage:        result.ModelUsage.Clone(),
		HasToolCalls: len(toolCalls) > 0,
	}); err != nil {
		return nil, err
	}
	result.RawResponse = raw

	messagesWithResponse := common.CloneAgenticMessages(args.Messages)
	messagesWithResponse = append(messagesWithResponse, raw)
	if len(toolCalls) > 0 {
		for _, toolCall := range toolCalls {
			if toolCall == nil {
				continue
			}
			messagesWithResponse = append(messagesWithResponse, common.FunctionToolResultMessage(&schema.FunctionToolResult{
				CallID: toolCall.CallID,
				Name:   toolCall.Name,
				Content: []*schema.FunctionToolResultContentBlock{
					{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: "..."}},
				},
			}))
		}
	}

	if !args.Compress || !compression.ShouldCompress(messagesWithResponse, a.modelMaxTokensK) {
		return result, nil
	}

	strategy := normalizedCompressionStrategy(args.CompressionOptions.Strategy)
	beforeMessages := len(args.Messages)
	if err := events.WriteWithContext(ctx, common.ContextCompressionStartedEvent{
		Strategy:       strategy,
		BeforeMessages: beforeMessages,
	}); err != nil {
		return nil, err
	}

	usesModel := strategy != common.CompressionStrategyDiscardHalf
	if usesModel {
		if err := events.WriteWithContext(ctx, common.ModelCallStartedEvent{
			Phase: common.ModelCallPhaseCompression,
		}); err != nil {
			return nil, err
		}
	}

	compressedMessages, promptTokens, completionTokens, cachedTokens, compressErr := compression.Compress(
		ctx,
		a.llmClient,
		args.Messages,
		args.CompressionOptions,
		opts...,
	)
	if compressErr != nil {
		if usesModel {
			if err := events.WriteWithContext(ctx, common.ModelCallFailedEvent{
				Phase: common.ModelCallPhaseCompression,
				Error: compressErr.Error(),
			}); err != nil {
				return nil, err
			}
		}
		if err := events.WriteWithContext(ctx, common.ContextCompressionFailedEvent{
			Strategy: strategy,
			Error:    compressErr.Error(),
		}); err != nil {
			return nil, err
		}
		logging.Errorf("Agent.think: failed to compress context: %v", compressErr)
		return result, nil
	}

	result.CompressionUsage = common.NewAgentUsage(promptTokens, cachedTokens, completionTokens)
	if usesModel {
		if err := events.WriteWithContext(ctx, common.ModelCallCompletedEvent{
			Phase: common.ModelCallPhaseCompression,
			Usage: result.CompressionUsage.Clone(),
		}); err != nil {
			return nil, err
		}
	}
	if err := events.WriteWithContext(ctx, common.ContextCompressionCompletedEvent{
		Strategy:       strategy,
		BeforeMessages: beforeMessages,
		AfterMessages:  len(compressedMessages),
	}); err != nil {
		return nil, err
	}

	result.IsCompressed = true
	result.CompressedMessages = compressedMessages
	result.Messages = compressedMessages
	return result, nil
}

func (a *Agent) streamModelResponse(
	ctx *common.AgentContext,
	messages []*schema.AgenticMessage,
	events streaming.Stream[common.AgentEvent],
	opts ...model.Option,
) (*schema.AgenticMessage, error) {
	reader, err := a.llmClient.Stream(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	chunks := make([]*schema.AgenticMessage, 0)
	for {
		chunk, recvErr := reader.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return nil, recvErr
		}
		if chunk == nil {
			continue
		}

		chunks = append(chunks, chunk)
		if delta := messageReasoning(chunk); delta != "" {
			if err := events.WriteWithContext(ctx, common.ReasoningDeltaEvent{Delta: delta}); err != nil {
				return nil, err
			}
		}
		if delta := assistantText(chunk); delta != "" {
			if err := events.WriteWithContext(ctx, common.AssistantTextDeltaEvent{Delta: delta}); err != nil {
				return nil, err
			}
		}
	}

	if len(chunks) == 0 {
		return nil, fmt.Errorf("model stream returned no messages")
	}
	message, err := schema.ConcatAgenticMessages(chunks)
	if err != nil {
		return nil, fmt.Errorf("concatenate model stream: %w", err)
	}
	return message, nil
}

func normalizedCompressionStrategy(strategy common.CompressionStrategy) common.CompressionStrategy {
	switch strategy {
	case common.CompressionStrategyPrecise, common.CompressionStrategyDiscardHalf:
		return strategy
	default:
		return common.CompressionStrategyAggressive
	}
}

func assistantMessageFromResponse(resp *schema.AgenticMessage) *schema.AgenticMessage {
	return resp
}
