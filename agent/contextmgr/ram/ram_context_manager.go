package ram

import (
	"context"
	"fmt"
	"sync"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/contextmgr"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
)

// RAMStore persists versioned context state in process memory.
type RAMStore struct {
	mu     sync.RWMutex
	states map[common.ContextUID]*contextmgr.State
}

var _ contextmgr.Store = (*RAMStore)(nil)

// NewRAMStore creates an empty in-process Store.
func NewRAMStore() *RAMStore {
	return &RAMStore{states: make(map[common.ContextUID]*contextmgr.State)}
}

// NewRAMContextManager creates a Manager backed by in-process storage.
func NewRAMContextManager() *contextmgr.Manager {
	return contextmgr.NewManager(NewRAMStore())
}

func (s *RAMStore) Create(ctx context.Context, state *contextmgr.State) (common.ContextUID, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var contextUID common.ContextUID
	for {
		contextUID = common.ContextUID(uuid.NewString())
		if _, exists := s.states[contextUID]; !exists {
			break
		}
	}
	next, err := cloneState(state)
	if err != nil {
		return "", err
	}
	next.Revision = 1
	s.states[contextUID] = next
	return contextUID, nil
}

func (s *RAMStore) Load(ctx context.Context, contextUID common.ContextUID) (*contextmgr.State, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	state, exists := s.states[contextUID]
	if !exists {
		return nil, contextmgr.ErrContextNotFound
	}
	return cloneState(state)
}

func (s *RAMStore) CompareAndSwap(
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

	current, exists := s.states[contextUID]
	if !exists {
		return contextmgr.ErrContextNotFound
	}
	if current.Revision != expectedRevision {
		return contextmgr.ErrRevisionConflict
	}
	next, err := cloneState(state)
	if err != nil {
		return err
	}
	next.Revision = expectedRevision + 1
	s.states[contextUID] = next
	return nil
}

func (s *RAMStore) Delete(ctx context.Context, contextUID common.ContextUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, contextUID)
	return nil
}

func cloneState(state *contextmgr.State) (*contextmgr.State, error) {
	payload, err := sonic.Marshal(state.Clone())
	if err != nil {
		return nil, fmt.Errorf("encode context state: %w", err)
	}
	var clone contextmgr.State
	if err := sonic.Unmarshal(payload, &clone); err != nil {
		return nil, fmt.Errorf("decode context state: %w", err)
	}
	return clone.Clone(), nil
}
