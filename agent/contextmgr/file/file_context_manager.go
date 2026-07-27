package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/contextmgr"
	"github.com/torrischen/goat/util/logging"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

const conversationStateVersion = 1

// FileContextManager manages conversation context using file storage.
type FileContextManager struct {
	mu    sync.RWMutex
	dir   string
	cache map[common.ContextUID]*conversationState
}

type conversationState struct {
	Version         int                      `json:"version"`
	Messages        []*schema.AgenticMessage `json:"messages"`
	PendingMessages []*schema.AgenticMessage `json:"pending_messages,omitempty"`
}

var _ contextmgr.ContextManager = (*FileContextManager)(nil)

// NewFileContextManager creates a file-backed context manager.
func NewFileContextManager(dir string) *FileContextManager {
	if dir == "" {
		dir = "data/conversations"
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		logging.Errorf("Failed to create conversation directory: %v", err)
	}

	return &FileContextManager{
		dir:   dir,
		cache: make(map[common.ContextUID]*conversationState),
	}
}

func (m *FileContextManager) InitNew(ctx context.Context) common.ContextUID {
	contextUID := m.NewContextUID(ctx)
	state := newConversationState()

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.persistState(contextUID, state); err != nil {
		logging.Errorf("Failed to persist new conversation: %v", err)
	}
	m.cache[contextUID] = state
	return contextUID
}

func (m *FileContextManager) NewContextUID(_ context.Context) common.ContextUID {
	return common.ContextUID(uuid.NewString())
}

func (m *FileContextManager) Append(_ context.Context, contextUID common.ContextUID, message *schema.AgenticMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, exists, err := m.stateLocked(contextUID)
	if err != nil {
		return err
	}
	if !exists {
		state = newConversationState()
	}

	next := cloneConversationState(state)
	next.Messages = append(next.Messages, message)
	if err := m.persistState(contextUID, next); err != nil {
		return err
	}
	m.cache[contextUID] = next
	return nil
}

func (m *FileContextManager) GetAll(_ context.Context, contextUID common.ContextUID) []*schema.AgenticMessage {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, exists, err := m.stateLocked(contextUID)
	if err != nil {
		logging.Errorf("Failed to load conversation %s: %v", contextUID, err)
		return []*schema.AgenticMessage{}
	}
	if !exists {
		return []*schema.AgenticMessage{}
	}
	return common.CloneAgenticMessages(state.Messages)
}

func (m *FileContextManager) Len(ctx context.Context, contextUID common.ContextUID) int {
	return len(m.GetAll(ctx, contextUID))
}

func (m *FileContextManager) Reset(_ context.Context, contextUID common.ContextUID, messages []*schema.AgenticMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, exists, err := m.stateLocked(contextUID)
	if err != nil {
		logging.Errorf("Failed to load conversation before reset: %v", err)
		return
	}
	if !exists {
		state = newConversationState()
	}

	next := cloneConversationState(state)
	next.Messages = common.CloneAgenticMessages(messages)
	if err := m.persistState(contextUID, next); err != nil {
		logging.Errorf("Failed to reset conversation: %v", err)
		return
	}
	m.cache[contextUID] = next
}

func (m *FileContextManager) EnqueuePendingMessages(
	_ context.Context,
	contextUID common.ContextUID,
	messages []*schema.AgenticMessage,
) error {
	if err := contextmgr.ValidatePendingMessages(messages); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	state, exists, err := m.stateLocked(contextUID)
	if err != nil {
		return err
	}
	if !exists {
		return contextmgr.ErrContextNotFound
	}
	if len(state.Messages) > 0 && contextmgr.IsFinalAnswerMessage(state.Messages[len(state.Messages)-1]) {
		return contextmgr.ErrConversationFinalized
	}

	next := cloneConversationState(state)
	next.PendingMessages = append(next.PendingMessages, common.CloneAgenticMessages(messages)...)
	if err := m.persistState(contextUID, next); err != nil {
		return err
	}
	m.cache[contextUID] = next
	return nil
}

func (m *FileContextManager) CommitTurn(
	_ context.Context,
	contextUID common.ContextUID,
	turnMessages []*schema.AgenticMessage,
) (*contextmgr.TurnCommitResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, exists, err := m.stateLocked(contextUID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, contextmgr.ErrContextNotFound
	}

	applied := common.CloneAgenticMessages(state.PendingMessages)
	next := cloneConversationState(state)
	next.Messages = append(next.Messages, common.CloneAgenticMessages(turnMessages)...)
	next.Messages = append(next.Messages, applied...)
	next.PendingMessages = nil
	if err := m.persistState(contextUID, next); err != nil {
		return nil, err
	}
	m.cache[contextUID] = next

	return &contextmgr.TurnCommitResult{
		AppliedPendingMessages: common.CloneAgenticMessages(applied),
	}, nil
}

func (m *FileContextManager) CommitFinal(
	_ context.Context,
	contextUID common.ContextUID,
	message *schema.AgenticMessage,
) error {
	if err := contextmgr.ValidateFinalMessage(message); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	state, exists, err := m.stateLocked(contextUID)
	if err != nil {
		return err
	}
	if !exists {
		return contextmgr.ErrContextNotFound
	}

	next := cloneConversationState(state)
	next.Messages = append(next.Messages, common.CloneAgenticMessages([]*schema.AgenticMessage{message})[0])
	next.PendingMessages = nil
	if err := m.persistState(contextUID, next); err != nil {
		return err
	}
	m.cache[contextUID] = next
	return nil
}

func (m *FileContextManager) Delete(_ context.Context, contextUID common.ContextUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.cache, contextUID)

	filePath := m.getFilePath(contextUID)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = os.Remove(filePath + ".tmp")
	return nil
}

func newConversationState() *conversationState {
	return &conversationState{
		Version:         conversationStateVersion,
		Messages:        []*schema.AgenticMessage{},
		PendingMessages: []*schema.AgenticMessage{},
	}
}

func cloneConversationState(state *conversationState) *conversationState {
	if state == nil {
		return newConversationState()
	}
	return &conversationState{
		Version:         conversationStateVersion,
		Messages:        common.CloneAgenticMessages(state.Messages),
		PendingMessages: common.CloneAgenticMessages(state.PendingMessages),
	}
}

// stateLocked returns the cached or persisted state. The caller must hold m.mu.
func (m *FileContextManager) stateLocked(contextUID common.ContextUID) (*conversationState, bool, error) {
	if state, exists := m.cache[contextUID]; exists {
		return state, true, nil
	}

	state, exists, err := m.loadState(contextUID)
	if err != nil || !exists {
		return state, exists, err
	}
	m.cache[contextUID] = state
	return state, true, nil
}

// Helper methods

func (m *FileContextManager) getFilePath(contextUID common.ContextUID) string {
	dateDir := filepath.Join(m.dir, time.Now().Format("2006-01-02"))
	fileName := string(contextUID) + ".jsonl"
	currentPath := filepath.Join(dateDir, fileName)
	if _, err := os.Stat(currentPath); err == nil {
		return currentPath
	}

	entries, err := os.ReadDir(m.dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		logging.Errorf("Failed to read conversation directory: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == filepath.Base(dateDir) {
			continue
		}
		candidate := filepath.Join(m.dir, entry.Name(), fileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	if err := os.MkdirAll(dateDir, 0755); err != nil {
		logging.Errorf("Failed to create date directory: %v", err)
	}
	return currentPath
}

// persistState writes committed history and the pending inbox together, so a
// successful rename advances both atomically.
func (m *FileContextManager) persistState(contextUID common.ContextUID, state *conversationState) error {
	filePath := m.getFilePath(contextUID)
	tmpPath := filePath + ".tmp"

	payload, err := sonic.Marshal(state)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(payload, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, filePath)
}

func (m *FileContextManager) loadState(contextUID common.ContextUID) (*conversationState, bool, error) {
	filePath := m.getFilePath(contextUID)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newConversationState(), false, nil
		}
		return nil, false, err
	}

	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return newConversationState(), true, nil
	}

	var state conversationState
	if err := sonic.UnmarshalString(trimmed, &state); err != nil {
		return nil, false, fmt.Errorf("decode conversation state: %w", err)
	}
	if state.Version != conversationStateVersion {
		return nil, false, fmt.Errorf(
			"unsupported conversation state version %d (expected %d)",
			state.Version,
			conversationStateVersion,
		)
	}
	if state.Messages == nil {
		state.Messages = []*schema.AgenticMessage{}
	}
	if state.PendingMessages == nil {
		state.PendingMessages = []*schema.AgenticMessage{}
	}
	return &state, true, nil
}
