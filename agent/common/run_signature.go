package common

import (
	"maps"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

const (
	// RunUIDExtraKey marks the explicit user message that starts an agent run.
	// The value is stored as a string so it has the same type before and after
	// context-manager serialization.
	RunUIDExtraKey = "goat.run_uid"

	// InternalToolContextUIDMetaKey exposes the current conversation ID to tools.
	InternalToolContextUIDMetaKey AgentDoMetaKey = "context_uid"
	// InternalToolRunUIDMetaKey exposes the current run ID to tools.
	InternalToolRunUIDMetaKey AgentDoMetaKey = "run_uid"
)

// RunUID uniquely identifies one invocation of Agent.Do.
type RunUID string

// NewRunUID creates a unique run identifier.
func NewRunUID() RunUID {
	return RunUID(uuid.NewString())
}

// String returns the underlying identifier.
func (id RunUID) String() string {
	return string(id)
}

// RunSignature identifies one run within a managed conversation.
type RunSignature struct {
	ContextUID ContextUID `json:"context_uid"`
	RunUID     RunUID     `json:"run_uid"`
}

// IsZero reports whether the signature is missing either identifier.
func (s RunSignature) IsZero() bool {
	return s.ContextUID == "" || s.RunUID == ""
}

// RunMessages is a contiguous run segment from a managed conversation.
type RunMessages struct {
	Signature RunSignature
	Messages  []*schema.AgenticMessage
}

// MarkRunStart attaches a run boundary to an explicit user message without
// adding model-visible content.
func MarkRunStart(message *schema.AgenticMessage, runUID RunUID) {
	if message == nil || runUID == "" {
		return
	}

	extra := maps.Clone(message.Extra)
	if extra == nil {
		extra = make(map[string]any, 1)
	}
	extra[RunUIDExtraKey] = runUID.String()
	message.Extra = extra
}

// RunUIDFromMessage returns the run boundary stored on an explicit user
// message. Tool-result messages also use the user role and are not boundaries.
func RunUIDFromMessage(message *schema.AgenticMessage) (RunUID, bool) {
	if message == nil || message.Role != schema.AgenticRoleTypeUser || message.Extra == nil {
		return "", false
	}
	for _, block := range message.ContentBlocks {
		if block != nil && block.FunctionToolResult != nil {
			return "", false
		}
	}

	value, ok := message.Extra[RunUIDExtraKey].(string)
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return "", false
	}
	return RunUID(value), true
}

// SplitMessagesByRun partitions retained conversation messages at explicit
// run boundaries. Messages before the first boundary, including the system
// prompt and legacy history, are returned as preamble.
func SplitMessagesByRun(
	contextUID ContextUID,
	messages []*schema.AgenticMessage,
) (preamble []*schema.AgenticMessage, runs []RunMessages) {
	preamble = make([]*schema.AgenticMessage, 0)
	runs = make([]RunMessages, 0)
	currentRun := -1

	for _, message := range messages {
		if runUID, ok := RunUIDFromMessage(message); ok {
			runs = append(runs, RunMessages{
				Signature: RunSignature{ContextUID: contextUID, RunUID: runUID},
				Messages:  make([]*schema.AgenticMessage, 0),
			})
			currentRun = len(runs) - 1
		}

		if currentRun < 0 {
			preamble = append(preamble, message)
			continue
		}
		runs[currentRun].Messages = append(runs[currentRun].Messages, message)
	}

	return preamble, runs
}

// RunSignatureFromContext returns the signature exposed to tools for the
// current run.
func RunSignatureFromContext(ctx *AgentContext) (RunSignature, bool) {
	if ctx == nil {
		return RunSignature{}, false
	}
	contextUID, contextOK := metaString(ctx.GetMeta(InternalToolContextUIDMetaKey))
	runUID, runOK := metaString(ctx.GetMeta(InternalToolRunUIDMetaKey))
	if !contextOK || !runOK {
		return RunSignature{}, false
	}
	return RunSignature{ContextUID: ContextUID(contextUID), RunUID: RunUID(runUID)}, true
}

func metaString(value any) (string, bool) {
	switch value := value.(type) {
	case string:
		value = strings.TrimSpace(value)
		return value, value != ""
	case ContextUID:
		return value.String(), value != ""
	case RunUID:
		return value.String(), value != ""
	default:
		return "", false
	}
}
