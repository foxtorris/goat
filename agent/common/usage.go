package common

type AgentUsage struct {
	PromptTokens     int
	CachedTokens     int
	CompletionTokens int
}

func NewAgentUsage(promptTokens, cachedTokens, completionTokens int) *AgentUsage {
	if promptTokens == 0 && cachedTokens == 0 && completionTokens == 0 {
		return nil
	}
	return &AgentUsage{
		PromptTokens:     promptTokens,
		CachedTokens:     cachedTokens,
		CompletionTokens: completionTokens,
	}
}

func (u *AgentUsage) Clone() *AgentUsage {
	if u == nil {
		return nil
	}
	return &AgentUsage{
		PromptTokens:     u.PromptTokens,
		CachedTokens:     u.CachedTokens,
		CompletionTokens: u.CompletionTokens,
	}
}

func (u *AgentUsage) Add(other *AgentUsage) {
	if u == nil || other == nil {
		return
	}
	u.PromptTokens += other.PromptTokens
	u.CachedTokens += other.CachedTokens
	u.CompletionTokens += other.CompletionTokens
}
