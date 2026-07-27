package file

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/contextmgr"
	"github.com/torrischen/goat/util"
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
// successful rename advances both atomically. Existing JSONL files are read by
// loadState and migrated to this envelope on their next mutation.
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
	if err := sonic.UnmarshalString(trimmed, &state); err == nil && state.Version == conversationStateVersion {
		if state.Messages == nil {
			state.Messages = []*schema.AgenticMessage{}
		}
		if state.PendingMessages == nil {
			state.PendingMessages = []*schema.AgenticMessage{}
		}
		return &state, true, nil
	}

	// Backward compatibility: older conversations are one AgenticMessage per
	// JSONL line. They are migrated to conversationState on the next write.
	messages := make([]*schema.AgenticMessage, 0)
	reader := bufio.NewReader(strings.NewReader(string(data)))
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, false, readErr
		}
		line = strings.TrimSpace(line)
		if line != "" {
			message, decodeErr := m.decodeMessage(line)
			if decodeErr != nil {
				return nil, false, decodeErr
			}
			messages = append(messages, message)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}

	return &conversationState{
		Version:         conversationStateVersion,
		Messages:        messages,
		PendingMessages: []*schema.AgenticMessage{},
	}, true, nil
}

type storedMessage struct {
	Role             string         `json:"role"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	Parts            []storedPart   `json:"parts"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type storedPart struct {
	Type string `json:"type"`

	// TextContent
	Text string `json:"text,omitempty"`

	// ImageURLContent
	URL    string `json:"url,omitempty"`
	Detail string `json:"detail,omitempty"`

	// BinaryContent
	MIMEType string `json:"mime_type,omitempty"`
	Data     string `json:"data,omitempty"` // base64 encoded

	// ToolCall
	ID               string `json:"id,omitempty"`
	ToolType         string `json:"tool_type,omitempty"`
	ThoughtSignature string `json:"thought_signature,omitempty"`
	FunctionCall     *struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function_call,omitempty"`

	// ToolCallResponse
	ToolCallID string `json:"tool_call_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Content    string `json:"content,omitempty"`
}

func (m *FileContextManager) encodeMessage(msg *schema.AgenticMessage) (string, error) {
	b, err := sonic.Marshal(msg)
	if err != nil {
		return "", err
	}
	return util.ByteToString(b), nil
}

func (m *FileContextManager) decodeMessage(line string) (*schema.AgenticMessage, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, errors.New("empty line")
	}

	var msg schema.AgenticMessage
	if err := sonic.UnmarshalString(line, &msg); err == nil &&
		msg.Role != "" &&
		(len(msg.ContentBlocks) > 0 || !strings.Contains(line, `"parts"`)) {
		return &msg, nil
	}

	var stored storedMessage
	if err := sonic.UnmarshalString(line, &stored); err != nil {
		return nil, err
	}

	converted := &schema.AgenticMessage{
		Role:          legacyRoleToAgenticRole(stored.Role),
		ContentBlocks: make([]*schema.ContentBlock, 0, len(stored.Parts)+1),
	}
	if stored.ReasoningContent != "" {
		converted.ContentBlocks = append(converted.ContentBlocks, common.ReasoningBlock(stored.ReasoningContent))
	}

	for _, sp := range stored.Parts {
		switch sp.Type {
		case "text":
			if converted.Role == schema.AgenticRoleTypeAssistant {
				converted.ContentBlocks = append(converted.ContentBlocks, common.AssistantTextBlock(sp.Text))
			} else {
				converted.ContentBlocks = append(converted.ContentBlocks, common.TextBlock(sp.Text))
			}
		case "image_url":
			converted.ContentBlocks = append(converted.ContentBlocks, common.ImageURLWithDetailBlock(sp.URL, sp.Detail))
		case "binary":
			converted.ContentBlocks = append(converted.ContentBlocks, common.Base64ImageBlock(sp.MIMEType, sp.Data))
		case "tool_call":
			if sp.FunctionCall != nil {
				converted.ContentBlocks = append(converted.ContentBlocks, schema.NewContentBlock(&schema.FunctionToolCall{
					CallID:    sp.ID,
					Name:      sp.FunctionCall.Name,
					Arguments: sp.FunctionCall.Arguments,
				}))
			}
		case "tool_call_response":
			converted.Role = schema.AgenticRoleTypeUser
			converted.ContentBlocks = append(converted.ContentBlocks, schema.NewContentBlock(&schema.FunctionToolResult{
				CallID: sp.ToolCallID,
				Name:   sp.Name,
				Content: []*schema.FunctionToolResultContentBlock{
					{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: sp.Content}},
				},
			}))
		default:
			logging.Warnf("Unknown stored part type: %s", sp.Type)
		}
	}

	return converted, nil
}

func legacyRoleToAgenticRole(role string) schema.AgenticRoleType {
	switch role {
	case "system":
		return schema.AgenticRoleTypeSystem
	case "ai":
		return schema.AgenticRoleTypeAssistant
	default:
		return schema.AgenticRoleTypeUser
	}
}
