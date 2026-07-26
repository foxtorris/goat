package common

import (
	"context"

	"github.com/torrischen/goat/streaming"

	"github.com/cloudwego/eino/components/model"
)

type AgentDoArgs struct {
	UserInput AgentUserInput
	// MemoryUID is the unique identifier for the memory
	// If set, the agent will load the memory to continue thinking.
	MemoryUID MemoryUID
	// SpecialRequirements will be appended to the system prompt and also used in final answer generation
	SpecialRequirements []string
	// Compress decides whether to compress the steps when context exceeds the limit
	Compress bool
	// CompressionOptions configures context compression for this run.
	CompressionOptions CompressionOptions
	// ContextMeta stores stateless context meta
	ContextMeta map[AgentDoMetaKey]any
	// the max step to run the agent
	MaxStep int
	// Callbacks contains the callbacks for the agent execution
	Callbacks *Callbacks
	// SkillUsageInstruction is the instruction to guide the agent on how to use skills
	SkillUsageInstruction string
	// PlanUsageInstruction is the instruction to guide originagent on when to create a plan and how granular the plan should be.
	// It is only used when EnablePlanning is true.
	PlanUsageInstruction string
	// FinalAnswerStreamingFunc is the function to stream the final answer, will be called when the agent finishes all steps
	FinalAnswerStreamingFunc FinalAnswerStreamFn
	// FinalAnswerWebhook sends the settled final answer payload to the configured URL after the final step is stored
	FinalAnswerWebhook *FinalAnswerWebhookConfig
	// EnablePlanning enables planning tools during execution so the agent can create and update a plan while completing the task.
	EnablePlanning bool
	// ToolExecutionOptions configures how the agent executes tools, it is only used when EnablePlanning is true.
	ToolExecutionOptions *ToolExecutionOptions
}

// CompressionStrategy controls the fidelity and compression ratio of context compaction.
type CompressionStrategy string

const (
	// CompressionStrategyPrecise checkpoints older detailed tool-process messages and preserves exact references.
	// System messages, user inputs, final answers, and skill-loading/reading messages remain raw.
	CompressionStrategyPrecise CompressionStrategy = "precise"
	// CompressionStrategyAggressive summarizes older detailed tool-process messages and retains recent context.
	// System messages, user inputs, final answers, and skill-loading/reading messages remain raw.
	CompressionStrategyAggressive CompressionStrategy = "aggressive"
	// CompressionStrategyDiscardHalf discards the oldest half of detailed tool-process messages without calling the model.
	// System messages, user inputs, final answers, and skill-loading/reading messages are preserved.
	CompressionStrategyDiscardHalf CompressionStrategy = "discard_half"
)

// CompressionOptions configures context compression for a single agent run.
type CompressionOptions struct {
	// Strategy selects the compaction algorithm.
	Strategy CompressionStrategy
	// RecentMessages overrides the number of raw recent messages retained.
	RecentMessages int
}

type FinalAnswerStreamFn func(context.Context, []byte) error

type CallbackFn func(*AgentContext, *Step)

type Callbacks struct {
	BeforeToolExecution CallbackFn
	AfterToolExecution  CallbackFn
}

type ToolExecutionOptions struct {
	EnableParallel bool
	MaxConcurrency int
}

type ThinkArgs struct {
	Interim               string
	UserInput             AgentUserInput
	Steps                 []*Step
	SpecialRequirements   []string
	Compress              bool
	SkillUsageInstruction string
	EnablePlanning        bool
}

type ThinkResult struct {
	NewStep                 *Step
	IsCompressed            bool
	Interim                 string
	CompressedPreviousSteps []*Step
	PromptTokens            int
	CompletionTokens        int
}

type Agent interface {
	// Do stores the current user input, starts the agent loop asynchronously,
	// and returns the memory UID and the step stream for this run. The stream is
	// closed when the run finishes, is interrupted, or stops with an error.
	Do(context.Context, *AgentDoArgs, ...model.Option) (MemoryUID, streaming.Stream[*Step], error)
}
