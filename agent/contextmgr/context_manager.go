// Package contextmgr defines conversation context management.
package contextmgr

import (
	"context"

	"github.com/torrischen/goat/agent/common"

	"github.com/cloudwego/eino/schema"
)

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

	// Reset replaces the entire conversation history (useful for compression).
	Reset(context.Context, common.ContextUID, []*schema.AgenticMessage)

	// Delete removes a conversation.
	Delete(context.Context, common.ContextUID) error
}
