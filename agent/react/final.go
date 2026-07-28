package react

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/util/logging"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// generateFinalAnswer generates a final answer when max steps are reached
func (a *Agent) generateFinalAnswer(
	ctx *common.AgentContext,
	messages []*schema.AgenticMessage,
	specialRequirements []string,
	streamingFunc func(context.Context, []byte) error,
	opts ...model.Option,
) (string, int, int, int) {
	// Build final answer prompt
	var promptText string
	if len(specialRequirements) > 0 {
		promptText = "Please provide a final answer to the user's question. Special requirements:\n"
		for i, req := range specialRequirements {
			promptText += fmt.Sprintf("%d. %s\n", i+1, req)
		}
	} else {
		promptText = "Please provide a final answer to the user's question based on the conversation history."
	}

	// Add prompt as a user message
	finalMessages := common.CloneAgenticMessages(messages)
	finalMessages = append(finalMessages, schema.UserAgenticMessage(promptText))

	// Remove tools from options for final answer
	finalOpts := make([]model.Option, 0, len(opts)+1)
	finalOpts = append(finalOpts, opts...)
	finalOpts = append(finalOpts, model.WithTools(nil))

	if streamingFunc != nil {
		return a.streamFinalAnswer(ctx, finalMessages, streamingFunc, finalOpts...)
	}

	raw, err := a.llmClient.Generate(ctx, finalMessages, finalOpts...)
	if err != nil {
		logging.Errorf("Agent.generateFinalAnswer error: %v", err)
		return "Error generating final answer: " + err.Error(), 0, 0, 0
	}

	if raw == nil {
		logging.Errorf("Agent.generateFinalAnswer error: return content length 0")
		return "Error: No response from model", 0, 0, 0
	}

	finalAnswer := assistantText(raw)

	promptTokens := 0
	cachedTokens := 0
	completionTokens := 0
	promptTokens, completionTokens, cachedTokens = messageTokens(raw)

	return finalAnswer, promptTokens, completionTokens, cachedTokens
}

func (a *Agent) streamFinalAnswer(
	ctx *common.AgentContext,
	messages []*schema.AgenticMessage,
	streamingFunc func(context.Context, []byte) error,
	opts ...model.Option,
) (string, int, int, int) {
	reader, err := a.llmClient.Stream(ctx, messages, opts...)
	if err != nil {
		logging.Errorf("Agent.streamFinalAnswer error: %v", err)
		return "Error generating final answer: " + err.Error(), 0, 0, 0
	}
	defer reader.Close()

	chunks := make([]*schema.AgenticMessage, 0)
	for {
		chunk, recvErr := reader.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			logging.Errorf("Agent.streamFinalAnswer receive error: %v", recvErr)
			return "Error generating final answer: " + recvErr.Error(), 0, 0, 0
		}
		if chunk == nil {
			continue
		}
		chunks = append(chunks, chunk)

		if delta := assistantText(chunk); delta != "" {
			if err := streamingFunc(ctx, []byte(delta)); err != nil {
				logging.Errorf("Agent.streamFinalAnswer callback error: %v", err)
				return "Error streaming final answer: " + err.Error(), 0, 0, 0
			}
		}
	}

	if len(chunks) == 0 {
		return "", 0, 0, 0
	}

	msg, err := schema.ConcatAgenticMessages(chunks)
	if err != nil {
		logging.Errorf("Agent.streamFinalAnswer concat error: %v", err)
		return "Error generating final answer: " + err.Error(), 0, 0, 0
	}

	promptTokens, completionTokens, cachedTokens := messageTokens(msg)
	return assistantText(msg), promptTokens, completionTokens, cachedTokens
}
