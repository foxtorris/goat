package react

import (
	"fmt"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/streaming"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func (a *Agent) generateFinalAnswer(
	ctx *common.AgentContext,
	messages []*schema.AgenticMessage,
	specialRequirements []string,
	events streaming.Stream[common.AgentEvent],
	opts ...model.Option,
) (string, *common.AgentUsage, error) {
	var promptText string
	if len(specialRequirements) > 0 {
		promptText = "Please provide a final answer to the user's question. Special requirements:\n"
		for i, requirement := range specialRequirements {
			promptText += fmt.Sprintf("%d. %s\n", i+1, requirement)
		}
	} else {
		promptText = "Please provide a final answer to the user's question based on the conversation history."
	}

	finalMessages := common.CloneAgenticMessages(messages)
	finalMessages = append(finalMessages, schema.UserAgenticMessage(promptText))
	finalOpts := append([]model.Option{}, opts...)
	finalOpts = append(finalOpts, model.WithTools(nil))

	if err := events.WriteWithContext(ctx, common.ModelCallStartedEvent{Phase: common.ModelCallPhaseFinal}); err != nil {
		return "", nil, err
	}
	raw, err := a.streamModelResponse(ctx, finalMessages, events, finalOpts...)
	if err != nil {
		if writeErr := events.WriteWithContext(ctx, common.ModelCallFailedEvent{
			Phase: common.ModelCallPhaseFinal,
			Error: err.Error(),
		}); writeErr != nil {
			return "", nil, fmt.Errorf("final model call failed: %v; write failure event: %w", err, writeErr)
		}
		return "", nil, fmt.Errorf("final model call: %w", err)
	}

	promptTokens, completionTokens, cachedTokens := messageTokens(raw)
	usage := common.NewAgentUsage(promptTokens, cachedTokens, completionTokens)
	if err := events.WriteWithContext(ctx, common.ModelCallCompletedEvent{
		Phase: common.ModelCallPhaseFinal,
		Usage: usage.Clone(),
	}); err != nil {
		return "", nil, err
	}
	return assistantText(raw), usage, nil
}
