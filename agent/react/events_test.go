package react

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/contextmgr/ram"
	"github.com/torrischen/goat/streaming"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type scriptedEventModel struct {
	mu        sync.Mutex
	responses [][]*schema.AgenticMessage
	inputs    [][]*schema.AgenticMessage
	streamErr error
}

func (m *scriptedEventModel) Generate(
	context.Context,
	[]*schema.AgenticMessage,
	...model.Option,
) (*schema.AgenticMessage, error) {
	return nil, errors.New("unexpected Generate call")
}

func (m *scriptedEventModel) Stream(
	_ context.Context,
	input []*schema.AgenticMessage,
	_ ...model.Option,
) (*schema.StreamReader[*schema.AgenticMessage], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputs = append(m.inputs, common.CloneAgenticMessages(input))
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	if len(m.responses) == 0 {
		return nil, errors.New("no scripted model response")
	}
	response := m.responses[0]
	m.responses = m.responses[1:]
	return schema.StreamReaderFromArray(response), nil
}

func TestDoEmitsDirectAnswerLifecycleAndUsage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	answer := messageWithUsage(common.AssistantTextMessage("done"), 7, 2, 3)
	agent := NewAgent(&scriptedEventModel{responses: [][]*schema.AgenticMessage{{answer}}}, 128, ram.NewRAMContextManager())
	_, eventStream, err := agent.Do(ctx, &common.AgentDoArgs{
		UserInput: common.AgentUserInput{Text: "answer directly"},
	})
	if err != nil {
		t.Fatal(err)
	}
	events := readAllEvents(t, ctx, eventStream)

	wantTypes := []common.AgentEventType{
		common.AgentEventTypeRunStarted,
		common.AgentEventTypeModelCallStarted,
		common.AgentEventTypeAssistantTextDelta,
		common.AgentEventTypeModelCallCompleted,
		common.AgentEventTypeFinalAnswerCompleted,
		common.AgentEventTypeRunCompleted,
	}
	if got := eventTypes(events); !reflect.DeepEqual(got, wantTypes) {
		t.Fatalf("event types = %v, want %v", got, wantTypes)
	}
	completed := events[len(events)-1].(common.RunCompletedEvent)
	if !reflect.DeepEqual(completed.Usage, &common.AgentUsage{
		PromptTokens: 7, CachedTokens: 2, CompletionTokens: 3,
	}) {
		t.Fatalf("run usage = %+v", completed.Usage)
	}
	if completed.IterationsUsed != 1 || completed.ToolCalls != 0 {
		t.Fatalf("run completed = %+v", completed)
	}
}

func TestDoEmitsRunFailedForBackgroundModelError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	agent := NewAgent(&scriptedEventModel{streamErr: errors.New("provider unavailable")}, 128, ram.NewRAMContextManager())
	_, eventStream, err := agent.Do(ctx, &common.AgentDoArgs{
		UserInput: common.AgentUserInput{Text: "fail"},
	})
	if err != nil {
		t.Fatal(err)
	}
	events := readAllEvents(t, ctx, eventStream)

	failedCalls := eventsByType[common.ModelCallFailedEvent](events)
	if len(failedCalls) != 1 || failedCalls[0].Error != "provider unavailable" {
		t.Fatalf("model failures = %+v", failedCalls)
	}
	terminals := terminalEvents(events)
	if len(terminals) != 1 {
		t.Fatalf("terminal events = %+v", terminals)
	}
	failed, ok := terminals[0].(common.RunFailedEvent)
	if !ok || failed.Operation != "think" || !containsAll(failed.Error, "think model call", "provider unavailable") {
		t.Fatalf("run failure = %+v", terminals[0])
	}
}

func TestDoStreamsParallelToolCompletionOrderAndKeepsResultOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	toolCalls := assistantToolCalls(
		&schema.FunctionToolCall{CallID: "slow-call", Name: "slow", Arguments: `{}`},
		&schema.FunctionToolCall{CallID: "fast-call", Name: "fast", Arguments: `{}`},
	)
	llm := &scriptedEventModel{responses: [][]*schema.AgenticMessage{
		{messageWithUsage(toolCalls, 5, 1, 2)},
		{messageWithUsage(common.AssistantTextMessage("finished"), 4, 0, 1)},
	}}
	agent := NewAgent(llm, 128, ram.NewRAMContextManager())
	releaseSlow := make(chan struct{})
	agent.AddTools(ctx,
		common.NewDefaultTool("slow", "slow tool", common.NewToolParameters(), func(*common.AgentContext, map[string]any) common.ToolResult {
			<-releaseSlow
			return common.NewDefaultToolResult("slow result")
		}),
		common.NewDefaultTool("fast", "fast tool", common.NewToolParameters(), func(*common.AgentContext, map[string]any) common.ToolResult {
			return common.NewDefaultToolResult("fast result")
		}),
	)

	_, eventStream, err := agent.Do(ctx, &common.AgentDoArgs{
		UserInput: common.AgentUserInput{Text: "use both tools"},
		ToolExecutionOptions: &common.ToolExecutionOptions{
			EnableParallel: true,
			MaxConcurrency: 2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	events := make([]common.AgentEvent, 0)
	released := false
	for {
		event, readErr := eventStream.ReadWithContext(ctx)
		if errors.Is(readErr, streaming.ErrStreamClosed) {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		events = append(events, event)
		if completed, ok := event.(common.ToolCallCompletedEvent); ok && completed.Name == "fast" && !released {
			close(releaseSlow)
			released = true
		}
	}

	requested := eventsByType[common.ToolCallRequestedEvent](events)
	if got := []string{requested[0].Name, requested[1].Name}; !reflect.DeepEqual(got, []string{"slow", "fast"}) {
		t.Fatalf("requested order = %v", got)
	}
	completed := eventsByType[common.ToolCallCompletedEvent](events)
	if got := []string{completed[0].Name, completed[1].Name}; !reflect.DeepEqual(got, []string{"fast", "slow"}) {
		t.Fatalf("completion order = %v", got)
	}

	llm.mu.Lock()
	secondInput := common.CloneAgenticMessages(llm.inputs[1])
	llm.mu.Unlock()
	toolResultNames := make([]string, 0, 2)
	for _, message := range secondInput {
		for _, block := range message.ContentBlocks {
			if block != nil && block.FunctionToolResult != nil {
				toolResultNames = append(toolResultNames, block.FunctionToolResult.Name)
			}
		}
	}
	if !reflect.DeepEqual(toolResultNames, []string{"slow", "fast"}) {
		t.Fatalf("model tool result order = %v", toolResultNames)
	}

	terminal := eventsByType[common.RunCompletedEvent](events)
	if len(terminal) != 1 || terminal[0].ToolCalls != 2 || !reflect.DeepEqual(terminal[0].Usage, &common.AgentUsage{
		PromptTokens: 9, CachedTokens: 1, CompletionTokens: 3,
	}) {
		t.Fatalf("run completed events = %+v", terminal)
	}
}

func TestDoEmitsInterruptedAfterWrappedTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	llm := &scriptedEventModel{responses: [][]*schema.AgenticMessage{{assistantToolCalls(
		&schema.FunctionToolCall{CallID: "approval-call", Name: "approval", Arguments: `{}`},
	)}}}
	agent := NewAgent(llm, 128, ram.NewRAMContextManager())
	agent.AddTool(ctx, common.InterruptLoopAfter(common.NewDefaultTool(
		"approval",
		"pause the run",
		common.NewToolParameters(),
		func(*common.AgentContext, map[string]any) common.ToolResult {
			return common.NewDefaultToolResult("waiting")
		},
	)))

	_, eventStream, err := agent.Do(ctx, &common.AgentDoArgs{UserInput: common.AgentUserInput{Text: "pause"}})
	if err != nil {
		t.Fatal(err)
	}
	events := readAllEvents(t, ctx, eventStream)
	if len(eventsByType[common.ToolCallCompletedEvent](events)) != 1 {
		t.Fatalf("missing tool completed event: %v", eventTypes(events))
	}
	if len(eventsByType[common.FinalAnswerCompletedEvent](events)) != 0 {
		t.Fatalf("interrupted run emitted a final answer: %v", eventTypes(events))
	}
	terminals := terminalEvents(events)
	if len(terminals) != 1 || terminals[0].Type() != common.AgentEventTypeRunInterrupted {
		t.Fatalf("terminal events = %+v", terminals)
	}
}

func TestDoEmitsCanceledTerminalAfterContextCancellation(t *testing.T) {
	baseCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	runCtx, cancel := context.WithCancel(baseCtx)
	defer cancel()

	llm := &scriptedEventModel{responses: [][]*schema.AgenticMessage{{assistantToolCalls(
		&schema.FunctionToolCall{CallID: "blocking-call", Name: "blocking", Arguments: `{}`},
	)}}}
	agent := NewAgent(llm, 128, ram.NewRAMContextManager())
	agent.AddTool(baseCtx, common.NewDefaultTool(
		"blocking",
		"wait for cancellation",
		common.NewToolParameters(),
		func(ctx *common.AgentContext, _ map[string]any) common.ToolResult {
			<-ctx.Done()
			return common.NewDefaultToolResult("canceled")
		},
	))

	_, eventStream, err := agent.Do(runCtx, &common.AgentDoArgs{
		UserInput: common.AgentUserInput{Text: "wait"},
	})
	if err != nil {
		t.Fatal(err)
	}

	events := make([]common.AgentEvent, 0)
	for {
		event, readErr := eventStream.ReadWithContext(baseCtx)
		if errors.Is(readErr, streaming.ErrStreamClosed) {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		events = append(events, event)
		if _, ok := event.(common.ToolCallStartedEvent); ok {
			cancel()
		}
	}

	terminals := terminalEvents(events)
	if len(terminals) != 1 {
		t.Fatalf("terminal events = %+v", terminals)
	}
	canceled, ok := terminals[0].(common.RunCanceledEvent)
	if !ok || canceled.Reason != context.Canceled.Error() {
		t.Fatalf("run canceled event = %+v", terminals[0])
	}
}

func TestDoTurnsNilToolResultIntoFailureEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	llm := &scriptedEventModel{responses: [][]*schema.AgenticMessage{
		{assistantToolCalls(&schema.FunctionToolCall{CallID: "nil-call", Name: "nil_result", Arguments: `{}`})},
		{common.AssistantTextMessage("recovered")},
	}}
	agent := NewAgent(llm, 128, ram.NewRAMContextManager())
	agent.AddTool(ctx, common.NewDefaultTool(
		"nil_result",
		"return no result",
		common.NewToolParameters(),
		func(*common.AgentContext, map[string]any) common.ToolResult { return nil },
	))

	_, eventStream, err := agent.Do(ctx, &common.AgentDoArgs{
		UserInput: common.AgentUserInput{Text: "handle nil"},
	})
	if err != nil {
		t.Fatal(err)
	}
	events := readAllEvents(t, ctx, eventStream)

	failures := eventsByType[common.ToolCallFailedEvent](events)
	if len(failures) != 1 || failures[0].Stage != common.ToolCallFailureStageExecution ||
		failures[0].Error != "tool returned a nil result" {
		t.Fatalf("tool failures = %+v", failures)
	}
	terminals := terminalEvents(events)
	if len(terminals) != 1 || terminals[0].Type() != common.AgentEventTypeRunCompleted {
		t.Fatalf("terminal events = %+v", terminals)
	}

	llm.mu.Lock()
	secondInput := common.CloneAgenticMessages(llm.inputs[1])
	llm.mu.Unlock()
	if !messageInputContains(secondInput, "Error: tool returned a nil result") {
		t.Fatalf("second model input does not contain the tool failure: %+v", secondInput)
	}
}

func messageWithUsage(message *schema.AgenticMessage, prompt, cached, completion int) *schema.AgenticMessage {
	message.ResponseMeta = &schema.AgenticResponseMeta{TokenUsage: &schema.TokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
		PromptTokenDetails: schema.PromptTokenDetails{
			CachedTokens: cached,
		},
	}}
	return message
}

func assistantToolCalls(calls ...*schema.FunctionToolCall) *schema.AgenticMessage {
	blocks := make([]*schema.ContentBlock, 0, len(calls))
	for _, call := range calls {
		blocks = append(blocks, schema.NewContentBlock(call))
	}
	return &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: blocks}
}

func eventTypes(events []common.AgentEvent) []common.AgentEventType {
	types := make([]common.AgentEventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type())
	}
	return types
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}

func messageInputContains(messages []*schema.AgenticMessage, value string) bool {
	for _, message := range messages {
		for _, block := range message.ContentBlocks {
			if block == nil || block.FunctionToolResult == nil {
				continue
			}
			for _, content := range block.FunctionToolResult.Content {
				if content != nil && content.Text != nil && strings.Contains(content.Text.Text, value) {
					return true
				}
			}
		}
	}
	return false
}
