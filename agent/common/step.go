package common

import (
	"strings"

	"github.com/torrischen/goat/util"
	"github.com/torrischen/goat/util/logging"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
)

// The optimization advice will affect the agent's next step's behavior
type Step struct {
	Thought            string                 `json:"thought"`
	Action             string                 `json:"action"`
	UseToolOrNot       bool                   `json:"use_tool_or_not"`
	ToolName           string                 `json:"tool_name"`
	ActionInputParam   map[string]any         `json:"action_input_param"`
	IsFinalAnswer      bool                   `json:"is_final_answer"`
	Observation        string                 `json:"observation"`
	ObservationImages  []*schema.ContentBlock `json:"-"`
	OptimizationAdvice *string                `json:"optimization_advice,omitempty"`
	IsCompressed       bool                   `json:"-"`
	// Usage is the total token usage associated with this step.
	Usage         *AgentUsage `json:"usage,omitempty"`
	ModelUsage    *AgentUsage `json:"model_usage,omitempty"`
	CallbackUsage *AgentUsage `json:"callback_usage,omitempty"`
}

func (s *Step) AddUsage(promptTokens, cachedTokens, completionTokens int) {
	if s == nil {
		return
	}
	addUsage(&s.Usage, NewAgentUsage(promptTokens, cachedTokens, completionTokens))
}

func (s *Step) AddModelUsage(promptTokens, cachedTokens, completionTokens int) {
	if s == nil {
		return
	}
	usage := NewAgentUsage(promptTokens, cachedTokens, completionTokens)
	addUsage(&s.ModelUsage, usage)
	addUsage(&s.Usage, usage)
}

func (s *Step) AddCallbackUsage(promptTokens, cachedTokens, completionTokens int) {
	if s == nil {
		return
	}
	usage := NewAgentUsage(promptTokens, cachedTokens, completionTokens)
	addUsage(&s.CallbackUsage, usage)
	addUsage(&s.Usage, usage)
}

func addUsage(dst **AgentUsage, usage *AgentUsage) {
	if usage == nil {
		return
	}
	if *dst == nil {
		*dst = usage.Clone()
		return
	}
	(*dst).Add(usage)
}

func NewStepFromString(s string) (*Step, error) {
	step := &Step{}

	decoder := sonic.ConfigDefault.NewDecoder(strings.NewReader(s))
	decoder.UseNumber()

	if err := decoder.Decode(step); err != nil {
		logging.Errorf("NewStepFromString error, the origin s is:  %v", s)
		return nil, err
	}

	return step, nil
}

func (s *Step) ToString() (string, error) {
	b, err := sonic.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}

	return util.ByteToString(b), nil
}

func (s *Step) ToPrompt() (*schema.AgenticMessage, error) {
	content, err := s.ToString()
	if err != nil {
		return nil, err
	}

	return AssistantTextMessage(content), nil
}
