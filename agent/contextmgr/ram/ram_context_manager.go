package ram

import (
	"context"
	"sync"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/contextmgr"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// RAMContextManager manages conversation context in process memory.
type RAMContextManager struct {
	mu       sync.RWMutex
	messages map[common.ContextUID][]*schema.AgenticMessage
}

var _ contextmgr.ContextManager = (*RAMContextManager)(nil)

// NewRAMContextManager creates an in-process context manager.
func NewRAMContextManager() *RAMContextManager {
	return &RAMContextManager{
		messages: make(map[common.ContextUID][]*schema.AgenticMessage),
	}
}

func (m *RAMContextManager) InitNew(ctx context.Context) common.ContextUID {
	contextUID := m.NewContextUID(ctx)

	m.mu.Lock()
	m.messages[contextUID] = []*schema.AgenticMessage{}
	m.mu.Unlock()

	return contextUID
}

func (m *RAMContextManager) NewContextUID(_ context.Context) common.ContextUID {
	return common.ContextUID(uuid.NewString())
}

func (m *RAMContextManager) Append(_ context.Context, contextUID common.ContextUID, message *schema.AgenticMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messages[contextUID] = append(m.messages[contextUID], message)
	return nil
}

func (m *RAMContextManager) GetAll(_ context.Context, contextUID common.ContextUID) []*schema.AgenticMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()

	messages := m.messages[contextUID]
	if len(messages) == 0 {
		return []*schema.AgenticMessage{}
	}

	// Return a copy to prevent external modification
	return common.CloneAgenticMessages(messages)
}

func (m *RAMContextManager) Len(_ context.Context, contextUID common.ContextUID) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.messages[contextUID])
}

func (m *RAMContextManager) Reset(_ context.Context, contextUID common.ContextUID, messages []*schema.AgenticMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messages[contextUID] = messages
}

func (m *RAMContextManager) Delete(_ context.Context, contextUID common.ContextUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.messages, contextUID)
	return nil
}
