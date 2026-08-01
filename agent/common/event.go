package common

import (
	"time"

	"github.com/cloudwego/eino/schema"
)

type AgentEventType string

const (
	AgentEventTypeRunStarted                  AgentEventType = "run_started"
	AgentEventTypeModelCallStarted            AgentEventType = "model_call_started"
	AgentEventTypeAssistantTextDelta          AgentEventType = "assistant_text_delta"
	AgentEventTypeModelCallCompleted          AgentEventType = "model_call_completed"
	AgentEventTypeModelCallFailed             AgentEventType = "model_call_failed"
	AgentEventTypeContextCompressionStarted   AgentEventType = "context_compression_started"
	AgentEventTypeContextCompressionCompleted AgentEventType = "context_compression_completed"
	AgentEventTypeContextCompressionFailed    AgentEventType = "context_compression_failed"
	AgentEventTypeToolCallRequested           AgentEventType = "tool_call_requested"
	AgentEventTypeToolCallStarted             AgentEventType = "tool_call_started"
	AgentEventTypeToolCallCompleted           AgentEventType = "tool_call_completed"
	AgentEventTypeToolCallFailed              AgentEventType = "tool_call_failed"
	AgentEventTypeSteeringApplied             AgentEventType = "steering_applied"
	AgentEventTypeFinalAnswerCompleted        AgentEventType = "final_answer_completed"
	AgentEventTypeRunCompleted                AgentEventType = "run_completed"
	AgentEventTypeRunInterrupted              AgentEventType = "run_interrupted"
	AgentEventTypeRunCanceled                 AgentEventType = "run_canceled"
	AgentEventTypeRunFailed                   AgentEventType = "run_failed"
)

type ModelCallPhase string

const (
	ModelCallPhaseThink       ModelCallPhase = "think"
	ModelCallPhaseCompression ModelCallPhase = "compression"
	ModelCallPhaseFinal       ModelCallPhase = "final"
)

type ToolCallFailureStage string

const (
	ToolCallFailureStageLookup    ToolCallFailureStage = "lookup"
	ToolCallFailureStageArguments ToolCallFailureStage = "arguments"
	ToolCallFailureStageExecution ToolCallFailureStage = "execution"
)

type AgentEvent interface {
	Type() AgentEventType
}

type RunStartedEvent struct {
	MaxStep int `json:"max_step"`
}

func (RunStartedEvent) Type() AgentEventType { return AgentEventTypeRunStarted }

type ModelCallStartedEvent struct {
	Phase ModelCallPhase `json:"phase"`
}

func (ModelCallStartedEvent) Type() AgentEventType { return AgentEventTypeModelCallStarted }

type AssistantTextDeltaEvent struct {
	Delta string `json:"delta"`
}

func (AssistantTextDeltaEvent) Type() AgentEventType { return AgentEventTypeAssistantTextDelta }

type ModelCallCompletedEvent struct {
	Phase        ModelCallPhase `json:"phase"`
	Usage        *AgentUsage    `json:"usage,omitempty"`
	HasToolCalls bool           `json:"has_tool_calls"`
}

func (ModelCallCompletedEvent) Type() AgentEventType { return AgentEventTypeModelCallCompleted }

type ModelCallFailedEvent struct {
	Phase ModelCallPhase `json:"phase"`
	Error string         `json:"error"`
}

func (ModelCallFailedEvent) Type() AgentEventType { return AgentEventTypeModelCallFailed }

type ContextCompressionStartedEvent struct {
	Strategy       CompressionStrategy `json:"strategy"`
	BeforeMessages int                 `json:"before_messages"`
}

func (ContextCompressionStartedEvent) Type() AgentEventType {
	return AgentEventTypeContextCompressionStarted
}

type ContextCompressionCompletedEvent struct {
	Strategy       CompressionStrategy `json:"strategy"`
	BeforeMessages int                 `json:"before_messages"`
	AfterMessages  int                 `json:"after_messages"`
}

func (ContextCompressionCompletedEvent) Type() AgentEventType {
	return AgentEventTypeContextCompressionCompleted
}

type ContextCompressionFailedEvent struct {
	Strategy CompressionStrategy `json:"strategy"`
	Error    string              `json:"error"`
}

func (ContextCompressionFailedEvent) Type() AgentEventType {
	return AgentEventTypeContextCompressionFailed
}

type ToolCallRequestedEvent struct {
	CallID    string         `json:"call_id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func (ToolCallRequestedEvent) Type() AgentEventType { return AgentEventTypeToolCallRequested }

type ToolCallStartedEvent struct {
	CallID    string         `json:"call_id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func (ToolCallStartedEvent) Type() AgentEventType { return AgentEventTypeToolCallStarted }

type ToolCallCompletedEvent struct {
	CallID   string                 `json:"call_id"`
	Name     string                 `json:"name"`
	Result   string                 `json:"result"`
	Images   []*schema.ContentBlock `json:"images,omitempty"`
	Duration time.Duration          `json:"duration"`
}

func (ToolCallCompletedEvent) Type() AgentEventType { return AgentEventTypeToolCallCompleted }

type ToolCallFailedEvent struct {
	CallID string               `json:"call_id"`
	Name   string               `json:"name"`
	Stage  ToolCallFailureStage `json:"stage"`
	Error  string               `json:"error"`
}

func (ToolCallFailedEvent) Type() AgentEventType { return AgentEventTypeToolCallFailed }

type SteeringAppliedEvent struct {
	Count int `json:"count"`
}

func (SteeringAppliedEvent) Type() AgentEventType { return AgentEventTypeSteeringApplied }

type FinalAnswerCompletedEvent struct {
	Answer string `json:"answer"`
}

func (FinalAnswerCompletedEvent) Type() AgentEventType { return AgentEventTypeFinalAnswerCompleted }

type RunCompletedEvent struct {
	Usage          *AgentUsage `json:"usage,omitempty"`
	IterationsUsed int         `json:"iterations_used"`
	ToolCalls      int         `json:"tool_calls"`
}

func (RunCompletedEvent) Type() AgentEventType { return AgentEventTypeRunCompleted }

type RunInterruptedEvent struct {
	Usage          *AgentUsage `json:"usage,omitempty"`
	IterationsUsed int         `json:"iterations_used"`
	Reason         string      `json:"reason"`
}

func (RunInterruptedEvent) Type() AgentEventType { return AgentEventTypeRunInterrupted }

type RunCanceledEvent struct {
	Usage          *AgentUsage `json:"usage,omitempty"`
	IterationsUsed int         `json:"iterations_used"`
	Reason         string      `json:"reason"`
}

func (RunCanceledEvent) Type() AgentEventType { return AgentEventTypeRunCanceled }

type RunFailedEvent struct {
	Usage          *AgentUsage `json:"usage,omitempty"`
	IterationsUsed int         `json:"iterations_used"`
	Operation      string      `json:"operation"`
	Error          string      `json:"error"`
}

func (RunFailedEvent) Type() AgentEventType { return AgentEventTypeRunFailed }

func IsTerminalAgentEvent(event AgentEvent) bool {
	if event == nil {
		return false
	}
	switch event.Type() {
	case AgentEventTypeRunCompleted,
		AgentEventTypeRunInterrupted,
		AgentEventTypeRunCanceled,
		AgentEventTypeRunFailed:
		return true
	default:
		return false
	}
}
