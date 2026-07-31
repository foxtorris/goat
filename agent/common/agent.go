package common

import (
	"context"

	"github.com/torrischen/goat/streaming"

	"github.com/cloudwego/eino/components/model"
)

type AgentSteerArgs struct {
	// ContextUID identifies the conversation whose pending inbox receives the
	// messages.
	ContextUID ContextUID
	// UserInputs are queued as separate user messages in the supplied order.
	UserInputs []AgentUserInput
}

type AgentDoArgs struct {
	UserInput AgentUserInput
	// ContextUID is the unique identifier for a conversation.
	// If set, the agent will load its managed context to continue thinking.
	ContextUID ContextUID
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
	// SkillsDir is the root directory used to discover and read skills for this
	// run. An empty value uses SkillDefaultFolder. The resolved value is exposed
	// to tools and callbacks through AgentContext metadata.
	SkillsDir string
	// SkillUsageInstruction is the instruction to guide the agent on how to use skills.
	SkillUsageInstruction string
	// PlanUsageInstruction guides the React agent on when to create a plan and how granular the plan should be.
	// It is only used when EnablePlanning is true.
	PlanUsageInstruction string
	// FinalAnswerStreamingFunc receives streamed final-answer chunks.
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
	// and returns the context UID and the step stream for this run. The stream is
	// closed when the run finishes, is interrupted, or stops with an error.
	Do(context.Context, *AgentDoArgs, ...model.Option) (ContextUID, streaming.Stream[*Step], error)

	// Steer queues one or more user messages for a conversation. Messages are
	// committed after the next complete non-final turn. A final answer discards
	// pending messages, and steering a finalized conversation returns an error.
	Steer(context.Context, *AgentSteerArgs) error
}
