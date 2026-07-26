package originagent

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/originagent/compression"
	"github.com/torrischen/goat/util/logging"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type ThinkArgs struct {
	UserInput                common.AgentUserInput
	SpecialRequirements      []string
	Compress                 bool
	CompressionOptions       common.CompressionOptions
	Messages                 []*schema.AgenticMessage
	SystemPrompt             string
	FinalAnswerStreamingFunc func(context.Context, []byte) error
}

type ThinkResult struct {
	RawResponse        *schema.AgenticMessage
	IsCompressed       bool
	CompressedMessages []*schema.AgenticMessage
	Messages           []*schema.AgenticMessage
	PromptTokens       int
	CachedTokens       int
	CompletionTokens   int
}

// Think generates the next step and determines if compression is needed
func (a *Agent) Think(ctx *common.AgentContext, args *ThinkArgs, opts ...model.Option) (*ThinkResult, error) {
	finalThinkResult := &ThinkResult{
		IsCompressed: false,
		Messages:     args.Messages,
	}
	promptTokens := 0
	cachedTokens := 0
	completionTokens := 0

	// First, call the LLM to get the next step. When a final answer streaming
	// callback is configured, read the model stream so direct final answers can
	// be forwarded incrementally instead of after Generate returns.
	raw, err := a.generateThinkResponse(ctx, args.Messages, args.FinalAnswerStreamingFunc, opts...)
	if err != nil {
		logging.Errorf("Agent.Think error: %v", err)
		return nil, err
	}

	if raw == nil {
		logging.Errorf("Agent.Think error: return content length 0")
		return nil, fmt.Errorf("return content length 0")
	}

	choicePromptTokens, choiceCompletionTokens, choiceCachedTokens := messageTokens(raw)
	promptTokens += choicePromptTokens
	completionTokens += choiceCompletionTokens
	cachedTokens += choiceCachedTokens

	finalThinkResult.RawResponse = raw

	// Now check if we need to compress by simulating the messages after this response
	messagesWithNewStep := common.CloneAgenticMessages(args.Messages)
	toolCalls := functionToolCalls(raw)

	// Add the assistant's response to messages (simulating what will happen next)
	if len(toolCalls) > 0 {
		// If there are tool calls, simulate the assistant message with tool calls.
		messagesWithNewStep = append(messagesWithNewStep, raw)

		// Simulate tool responses (rough estimation for token counting)
		for _, tc := range toolCalls {
			messagesWithNewStep = append(messagesWithNewStep, common.FunctionToolResultMessage(&schema.FunctionToolResult{
				CallID: tc.CallID,
				Name:   tc.Name,
				Content: []*schema.FunctionToolResultContentBlock{
					{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: "..."}},
				},
			}))
		}
	} else {
		// If no tool calls, add the final answer message
		messagesWithNewStep = append(messagesWithNewStep, raw)
	}

	// Check if the messages with new step exceed token limit
	if args.Compress && compression.ShouldCompress(messagesWithNewStep, a.modelMaxTokensK) {
		logging.Infof("Agent.Think: Context size will exceed limit after this step, compressing...")

		compressedMessages, compressPromptTokens, compressCompletionTokens, compressCachedTokens, err := compression.Compress(
			ctx,
			a.llmClient,
			args.Messages,
			args.CompressionOptions,
			opts...,
		)
		if err != nil {
			logging.Errorf("Agent.Think: Failed to compress context: %v", err)
		} else {
			promptTokens += compressPromptTokens
			completionTokens += compressCompletionTokens
			cachedTokens += compressCachedTokens

			finalThinkResult.IsCompressed = true
			finalThinkResult.CompressedMessages = compressedMessages
			finalThinkResult.Messages = compressedMessages
		}
	}

	finalThinkResult.PromptTokens = promptTokens
	finalThinkResult.CachedTokens = cachedTokens
	finalThinkResult.CompletionTokens = completionTokens

	return finalThinkResult, nil
}

func (a *Agent) generateThinkResponse(
	ctx *common.AgentContext,
	messages []*schema.AgenticMessage,
	streamingFunc func(context.Context, []byte) error,
	opts ...model.Option,
) (*schema.AgenticMessage, error) {
	if streamingFunc == nil {
		return a.llmClient.Generate(ctx, messages, opts...)
	}

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
		if delta := assistantText(chunk); delta != "" {
			if err := streamingFunc(ctx, []byte(delta)); err != nil {
				return nil, err
			}
		}
	}

	if len(chunks) == 0 {
		return nil, nil
	}

	return schema.ConcatAgenticMessages(chunks)
}

func assistantMessageFromResponse(resp *schema.AgenticMessage) *schema.AgenticMessage {
	return resp
}
