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

// FileStore persists one complete versioned State per conversation using an
// atomic temporary-file rename.
type FileStore struct {
	mu  *sync.Mutex
	dir string
}

var directoryLocks sync.Map

type conversationState struct {
	Version         int                                      `json:"version"`
	Revision        uint64                                   `json:"revision"`
	Messages        []*schema.AgenticMessage                 `json:"messages"`
	PendingMessages []*schema.AgenticMessage                 `json:"pending_messages,omitempty"`
	RunSnapshots    map[common.RunUID]contextmgr.RunSnapshot `json:"run_snapshots,omitempty"`
}

var _ contextmgr.Store = (*FileStore)(nil)

// NewFileStore creates a file-backed Store. An empty path uses
// data/conversations.
func NewFileStore(dir string) *FileStore {
	if dir == "" {
		dir = "data/conversations"
	}
	if absolute, err := filepath.Abs(dir); err == nil {
		dir = absolute
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logging.Errorf("Failed to create conversation directory: %v", err)
	}
	lock, _ := directoryLocks.LoadOrStore(dir, &sync.Mutex{})
	return &FileStore{
		dir: dir,
		mu:  lock.(*sync.Mutex),
	}
}

// NewFileContextManager creates a Manager backed by atomic local files.
func NewFileContextManager(dir string) *contextmgr.Manager {
	return contextmgr.NewManager(NewFileStore(dir))
}

func (s *FileStore) Create(ctx context.Context, state *contextmgr.State) (common.ContextUID, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	contextUID := common.ContextUID(uuid.NewString())
	next := state.Clone()
	next.Revision = 1
	if err := s.persistState(contextUID, next); err != nil {
		return "", err
	}
	return contextUID, nil
}

func (s *FileStore) Load(ctx context.Context, contextUID common.ContextUID) (*contextmgr.State, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, exists, err := s.loadState(contextUID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, contextmgr.ErrContextNotFound
	}
	return state.Clone(), nil
}

func (s *FileStore) CompareAndSwap(
	ctx context.Context,
	contextUID common.ContextUID,
	expectedRevision uint64,
	state *contextmgr.State,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists, err := s.loadState(contextUID)
	if err != nil {
		return err
	}
	if !exists {
		return contextmgr.ErrContextNotFound
	}
	if current.Revision != expectedRevision {
		return contextmgr.ErrRevisionConflict
	}
	next := state.Clone()
	next.Revision = expectedRevision + 1
	if err := s.persistState(contextUID, next); err != nil {
		return err
	}
	return nil
}

func (s *FileStore) Delete(ctx context.Context, contextUID common.ContextUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.getFilePath(contextUID)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = os.Remove(filePath + ".tmp")
	return nil
}

func stateToFile(state *contextmgr.State) *conversationState {
	clone := state.Clone()
	return &conversationState{
		Version:         conversationStateVersion,
		Revision:        clone.Revision,
		Messages:        clone.Messages,
		PendingMessages: clone.PendingMessages,
		RunSnapshots:    clone.RunSnapshots,
	}
}

func (s *conversationState) toState() *contextmgr.State {
	if s == nil {
		return contextmgr.NewState(nil)
	}
	state := (&contextmgr.State{
		Revision:        s.Revision,
		Messages:        s.Messages,
		PendingMessages: s.PendingMessages,
		RunSnapshots:    s.RunSnapshots,
	}).Clone()
	if state.Revision == 0 {
		state.Revision = 1
	}
	return state
}

func (s *FileStore) getFilePath(contextUID common.ContextUID) string {
	dateDir := filepath.Join(s.dir, time.Now().Format("2006-01-02"))
	fileName := string(contextUID) + ".jsonl"
	currentPath := filepath.Join(dateDir, fileName)
	if _, err := os.Stat(currentPath); err == nil {
		return currentPath
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		logging.Errorf("Failed to read conversation directory: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == filepath.Base(dateDir) {
			continue
		}
		candidate := filepath.Join(s.dir, entry.Name(), fileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		logging.Errorf("Failed to create date directory: %v", err)
	}
	return currentPath
}

func (s *FileStore) persistState(contextUID common.ContextUID, state *contextmgr.State) error {
	filePath := s.getFilePath(contextUID)
	tmpPath := filePath + ".tmp"
	payload, err := sonic.Marshal(stateToFile(state))
	if err != nil {
		return err
	}

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
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

func (s *FileStore) loadState(contextUID common.ContextUID) (*contextmgr.State, bool, error) {
	filePath := s.getFilePath(contextUID)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		state := contextmgr.NewState(nil)
		state.Revision = 1
		return state, true, nil
	}

	var persisted conversationState
	if err := sonic.UnmarshalString(trimmed, &persisted); err != nil {
		return nil, false, fmt.Errorf("decode conversation state: %w", err)
	}
	if persisted.Version != conversationStateVersion {
		return nil, false, fmt.Errorf(
			"unsupported conversation state version %d (expected %d)",
			persisted.Version,
			conversationStateVersion,
		)
	}
	return persisted.toState(), true, nil
}
