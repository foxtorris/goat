package ram

import (
	"context"
	"sync"

	"github.com/torrischen/goat/agent/common"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

type RAMMemory struct {
	mu       sync.RWMutex
	messages map[common.MemoryUID][]*schema.AgenticMessage
}

var _ common.Memory = (*RAMMemory)(nil)

func NewRAMMemory() *RAMMemory {
	return &RAMMemory{
		messages: make(map[common.MemoryUID][]*schema.AgenticMessage),
	}
}

func (m *RAMMemory) InitNew(ctx context.Context) common.MemoryUID {
	memoryUID := m.NewMemoryUID(ctx)

	m.mu.Lock()
	m.messages[memoryUID] = []*schema.AgenticMessage{}
	m.mu.Unlock()

	return memoryUID
}

func (m *RAMMemory) NewMemoryUID(_ context.Context) common.MemoryUID {
	return common.MemoryUID(uuid.NewString())
}

func (m *RAMMemory) Append(_ context.Context, memoryUID common.MemoryUID, message *schema.AgenticMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messages[memoryUID] = append(m.messages[memoryUID], message)
	return nil
}

func (m *RAMMemory) GetAll(_ context.Context, memoryUID common.MemoryUID) []*schema.AgenticMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()

	messages := m.messages[memoryUID]
	if len(messages) == 0 {
		return []*schema.AgenticMessage{}
	}

	// Return a copy to prevent external modification
	return common.CloneAgenticMessages(messages)
}

func (m *RAMMemory) Len(_ context.Context, memoryUID common.MemoryUID) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.messages[memoryUID])
}

func (m *RAMMemory) Reset(_ context.Context, memoryUID common.MemoryUID, messages []*schema.AgenticMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messages[memoryUID] = messages
}

func (m *RAMMemory) Delete(_ context.Context, memoryUID common.MemoryUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.messages, memoryUID)
	return nil
}
