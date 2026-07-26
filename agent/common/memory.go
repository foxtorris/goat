package common

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

type MemoryUID string

func (m MemoryUID) String() string {
	return string(m)
}

// Memory stores conversation history as AgenticMessage.
// This is the source of truth for multi-turn conversations
type Memory interface {
	// InitNew creates a new conversation and returns its ID
	InitNew(context.Context) MemoryUID

	// NewMemoryUID generates a new unique memory ID
	NewMemoryUID(context.Context) MemoryUID

	// Append adds a message to the conversation history
	Append(context.Context, MemoryUID, *schema.AgenticMessage) error

	// GetAll retrieves all messages in the conversation
	GetAll(context.Context, MemoryUID) []*schema.AgenticMessage

	// Len returns the number of messages in the conversation
	Len(context.Context, MemoryUID) int

	// Reset replaces the entire conversation history (useful for compression)
	Reset(context.Context, MemoryUID, []*schema.AgenticMessage)

	// Delete removes a conversation
	Delete(context.Context, MemoryUID) error
}
