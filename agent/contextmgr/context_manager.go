// Package contextmgr defines conversation context management.
package contextmgr

import (
	"context"
	"errors"
	"fmt"

	"github.com/torrischen/goat/agent/common"

	"github.com/cloudwego/eino/schema"
)

var (
	// ErrContextNotFound is returned when pending messages target a conversation
	// that has not been initialized.
	ErrContextNotFound = errors.New("context not found")
	// ErrInvalidPendingMessage is returned when the pending inbox receives a
	// nil or non-user message.
	ErrInvalidPendingMessage = errors.New("pending message must be a non-nil user message")
	// ErrConversationFinalized is returned when steering targets a conversation
	// whose latest committed message is a final assistant answer.
	ErrConversationFinalized = errors.New("conversation already has a final answer")
	// ErrInvalidFinalMessage is returned when CommitFinal receives a message
	// that is not an assistant answer.
	ErrInvalidFinalMessage = errors.New("final message must be a non-nil assistant answer")
)

// TurnCommitResult describes pending messages atomically applied after a
// committed assistant turn.
type TurnCommitResult struct {
	AppliedPendingMessages []*schema.AgenticMessage
}

// ValidatePendingMessages validates messages before they enter a conversation's
// pending inbox. An empty batch is a valid no-op.
func ValidatePendingMessages(messages []*schema.AgenticMessage) error {
	for i, message := range messages {
		if message == nil || message.Role != schema.AgenticRoleTypeUser {
			return fmt.Errorf("%w at index %d", ErrInvalidPendingMessage, i)
		}
	}
	return nil
}

// IsFinalAnswerMessage reports whether message is an assistant response without
// client-side function calls. This mirrors originagent's final-answer boundary.
func IsFinalAnswerMessage(message *schema.AgenticMessage) bool {
	if message == nil || message.Role != schema.AgenticRoleTypeAssistant {
		return false
	}
	for _, block := range message.ContentBlocks {
		if block != nil && block.FunctionToolCall != nil {
			return false
		}
	}
	return true
}

// ValidateFinalMessage validates a message supplied to CommitFinal.
func ValidateFinalMessage(message *schema.AgenticMessage) error {
	if !IsFinalAnswerMessage(message) {
		return ErrInvalidFinalMessage
	}
	return nil
}

// ContextManager stores conversation history as AgenticMessage values.
// It is the source of truth for multi-turn conversations.
type ContextManager interface {
	// InitNew creates a new conversation and returns its ID.
	InitNew(context.Context) common.ContextUID

	// NewContextUID generates a new unique conversation ID.
	NewContextUID(context.Context) common.ContextUID

	// Append adds a message to the conversation history.
	Append(context.Context, common.ContextUID, *schema.AgenticMessage) error

	// GetAll retrieves all messages in the conversation.
	GetAll(context.Context, common.ContextUID) []*schema.AgenticMessage

	// Len returns the number of messages in the conversation.
	Len(context.Context, common.ContextUID) int

	// Reset replaces the committed conversation history (useful for
	// compression). It must not remove pending messages.
	Reset(context.Context, common.ContextUID, []*schema.AgenticMessage)

	// EnqueuePendingMessages atomically adds user messages to the conversation's
	// pending inbox. The messages are not visible through GetAll until a
	// CommitTurn applies them. Implementations must preserve batch order.
	EnqueuePendingMessages(context.Context, common.ContextUID, []*schema.AgenticMessage) error

	// CommitTurn atomically appends turnMessages to committed history, then moves
	// every currently pending message to history immediately after that turn.
	// Calling CommitTurn with no turn messages flushes the current inbox.
	CommitTurn(context.Context, common.ContextUID, []*schema.AgenticMessage) (*TurnCommitResult, error)

	// CommitFinal atomically appends a final assistant answer and discards every
	// currently pending message. Subsequent EnqueuePendingMessages calls fail
	// until a new user message is appended by the next Do call.
	CommitFinal(context.Context, common.ContextUID, *schema.AgenticMessage) error

	// Delete removes a conversation and all of its pending messages.
	Delete(context.Context, common.ContextUID) error
}
