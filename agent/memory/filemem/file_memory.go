package filemem

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
	"github.com/torrischen/goat/util"
	"github.com/torrischen/goat/util/logging"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// FileMemory implements Memory using file storage
type FileMemory struct {
	mu    sync.RWMutex
	dir   string
	cache map[common.MemoryUID][]*schema.AgenticMessage
}

var _ common.Memory = (*FileMemory)(nil)

func NewFileMemory(dir string) *FileMemory {
	if dir == "" {
		dir = "data/conversations"
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		logging.Errorf("Failed to create conversation directory: %v", err)
	}

	return &FileMemory{
		dir:   dir,
		cache: make(map[common.MemoryUID][]*schema.AgenticMessage),
	}
}

func (m *FileMemory) InitNew(ctx context.Context) common.MemoryUID {
	muid := m.NewMemoryUID(ctx)

	m.mu.Lock()
	m.cache[muid] = []*schema.AgenticMessage{}
	m.mu.Unlock()

	// Create empty file
	if err := m.persist(muid, []*schema.AgenticMessage{}); err != nil {
		logging.Errorf("Failed to persist new conversation: %v", err)
	}

	return muid
}

func (m *FileMemory) NewMemoryUID(_ context.Context) common.MemoryUID {
	return common.MemoryUID(uuid.NewString())
}

func (m *FileMemory) Append(ctx context.Context, muid common.MemoryUID, message *schema.AgenticMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	messages, exists := m.cache[muid]
	if !exists {
		// Try to load from disk
		loaded, err := m.load(muid)
		if err != nil {
			return err
		}
		messages = loaded
	}

	messages = append(messages, message)
	m.cache[muid] = messages

	// Persist to disk
	return m.persist(muid, messages)
}

func (m *FileMemory) GetAll(ctx context.Context, muid common.MemoryUID) []*schema.AgenticMessage {
	m.mu.RLock()
	messages, exists := m.cache[muid]
	m.mu.RUnlock()

	if exists {
		// Return a copy to prevent external modification
		return common.CloneAgenticMessages(messages)
	}

	// Try to load from disk
	loaded, err := m.load(muid)
	if err != nil {
		logging.Errorf("Failed to load conversation %s: %v", muid, err)
		return []*schema.AgenticMessage{}
	}

	m.mu.Lock()
	m.cache[muid] = loaded
	m.mu.Unlock()

	return common.CloneAgenticMessages(loaded)
}

func (m *FileMemory) Len(ctx context.Context, muid common.MemoryUID) int {
	return len(m.GetAll(ctx, muid))
}

func (m *FileMemory) Reset(ctx context.Context, muid common.MemoryUID, messages []*schema.AgenticMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cache[muid] = messages
	if err := m.persist(muid, messages); err != nil {
		logging.Errorf("Failed to reset conversation: %v", err)
	}
}

func (m *FileMemory) Delete(ctx context.Context, muid common.MemoryUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.cache, muid)

	filePath := m.getFilePath(muid)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// Helper methods

func (m *FileMemory) getFilePath(muid common.MemoryUID) string {
	dateDir := filepath.Join(m.dir, time.Now().Format("2006-01-02"))
	fileName := string(muid) + ".jsonl"
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

func (m *FileMemory) persist(muid common.MemoryUID, messages []*schema.AgenticMessage) error {
	filePath := m.getFilePath(muid)
	tmpPath := filePath + ".tmp"

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	writer := bufio.NewWriter(f)
	for _, msg := range messages {
		encoded, err := m.encodeMessage(msg)
		if err != nil {
			_ = f.Close()
			return err
		}
		if _, err := writer.WriteString(encoded + "\n"); err != nil {
			_ = f.Close()
			return err
		}
	}

	if err := writer.Flush(); err != nil {
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

func (m *FileMemory) load(muid common.MemoryUID) ([]*schema.AgenticMessage, error) {
	filePath := m.getFilePath(muid)

	f, err := os.Open(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []*schema.AgenticMessage{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var messages []*schema.AgenticMessage
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line != "" {
			msg, decodeErr := m.decodeMessage(line)
			if decodeErr != nil {
				logging.Errorf("Failed to decode message: %v", decodeErr)
			} else {
				messages = append(messages, msg)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}

	return messages, nil
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

func (m *FileMemory) encodeMessage(msg *schema.AgenticMessage) (string, error) {
	b, err := sonic.Marshal(msg)
	if err != nil {
		return "", err
	}
	return util.ByteToString(b), nil
}

func (m *FileMemory) decodeMessage(line string) (*schema.AgenticMessage, error) {
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
