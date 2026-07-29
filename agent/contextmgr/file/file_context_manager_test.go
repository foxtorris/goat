package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/contextmgr"
)

func TestFileContextManagerLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	m := NewFileContextManager(dir)
	id := m.InitNew(ctx)
	if id == "" || m.NewContextUID(ctx) == id || m.Len(ctx, id) != 0 {
		t.Fatal("initialization failed")
	}
	if err := m.Append(ctx, id, schema.UserAgenticMessage("hello")); err != nil {
		t.Fatal(err)
	}
	messages := m.GetAll(ctx, id)
	messages[0] = nil
	if m.GetAll(ctx, id)[0] == nil {
		t.Fatal("GetAll returned backing slice")
	}
	if got := m.GetAll(ctx, common.ContextUID("missing")); got == nil || len(got) != 0 {
		t.Fatalf("missing context = %#v", got)
	}

	pending := []*schema.AgenticMessage{schema.UserAgenticMessage("steer")}
	if err := m.EnqueuePendingMessages(ctx, id, pending); err != nil {
		t.Fatal(err)
	}
	m.Reset(ctx, id, []*schema.AgenticMessage{schema.SystemAgenticMessage("system")})
	result, err := m.CommitTurn(ctx, id, []*schema.AgenticMessage{common.AssistantTextMessage("turn")})
	if err != nil || len(result.AppliedPendingMessages) != 1 || m.Len(ctx, id) != 3 {
		t.Fatalf("CommitTurn = %+v, %v", result, err)
	}

	missing := common.ContextUID("missing")
	if err := m.EnqueuePendingMessages(ctx, missing, pending); !errors.Is(err, contextmgr.ErrContextNotFound) {
		t.Fatalf("missing enqueue = %v", err)
	}
	if _, err := m.CommitTurn(ctx, missing, nil); !errors.Is(err, contextmgr.ErrContextNotFound) {
		t.Fatalf("missing commit = %v", err)
	}
	if err := m.CommitFinal(ctx, id, schema.UserAgenticMessage("bad")); !errors.Is(err, contextmgr.ErrInvalidFinalMessage) {
		t.Fatalf("invalid final = %v", err)
	}
	if err := m.CommitFinal(ctx, missing, common.AssistantTextMessage("final")); !errors.Is(err, contextmgr.ErrContextNotFound) {
		t.Fatalf("missing final = %v", err)
	}
	if err := m.EnqueuePendingMessages(ctx, id, pending); err != nil {
		t.Fatal(err)
	}
	if err := m.CommitFinal(ctx, id, common.AssistantTextMessage("final")); err != nil {
		t.Fatal(err)
	}
	if err := m.EnqueuePendingMessages(ctx, id, pending); !errors.Is(err, contextmgr.ErrConversationFinalized) {
		t.Fatalf("enqueue after final = %v", err)
	}

	// A fresh manager must read committed state from disk.
	reloaded := NewFileContextManager(dir)
	if reloaded.Len(ctx, id) != 4 {
		t.Fatalf("reloaded len = %d", reloaded.Len(ctx, id))
	}
	if err := reloaded.Delete(ctx, id); err != nil || reloaded.Len(ctx, id) != 0 {
		t.Fatalf("Delete = %v", err)
	}
	if err := reloaded.Delete(ctx, id); err != nil {
		t.Fatalf("repeated Delete = %v", err)
	}
}

func TestConversationStateHelpers(t *testing.T) {
	empty := newConversationState()
	if empty.Version != conversationStateVersion || empty.Messages == nil || empty.PendingMessages == nil {
		t.Fatalf("new state = %+v", empty)
	}
	if got := cloneConversationState(nil); got.Version != conversationStateVersion {
		t.Fatalf("clone nil = %+v", got)
	}
	original := &conversationState{Messages: []*schema.AgenticMessage{schema.UserAgenticMessage("x")}}
	clone := cloneConversationState(original)
	clone.Messages[0] = nil
	if original.Messages[0] == nil {
		t.Fatal("clone aliases message slice")
	}
}

func TestLoadStateVariantsAndOldDateLookup(t *testing.T) {
	dir := t.TempDir()
	m := NewFileContextManager(dir)
	id := common.ContextUID("stored")
	oldDir := filepath.Join(dir, "2000-01-01")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(oldDir, "stored.jsonl")
	if err := os.WriteFile(path, []byte(`{"version":1,"messages":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := m.getFilePath(id); got != path {
		t.Fatalf("old date path = %q, want %q", got, path)
	}
	state, exists, err := m.loadState(id)
	if err != nil || !exists || state.Messages == nil || state.PendingMessages == nil {
		t.Fatalf("load state = %+v, %v, %v", state, exists, err)
	}

	emptyID := common.ContextUID("empty")
	emptyPath := filepath.Join(dir, time.Now().Format("2006-01-02"), "empty.jsonl")
	if err := os.MkdirAll(filepath.Dir(emptyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(emptyPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if state, exists, err := m.loadState(emptyID); err != nil || !exists || state.Version != 1 {
		t.Fatalf("empty state = %+v, %v, %v", state, exists, err)
	}

	for name, payload := range map[string]string{
		"bad-json":    "not json",
		"bad-version": `{"version":2,"messages":[]}`,
	} {
		id := common.ContextUID(name)
		path := filepath.Join(dir, time.Now().Format("2006-01-02"), name+".jsonl")
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := m.loadState(id); err == nil {
			t.Fatalf("%s unexpectedly loaded", name)
		}
	}

	missing, exists, err := m.loadState(common.ContextUID("absent"))
	if err != nil || exists || missing.Version != 1 {
		t.Fatalf("missing state = %+v, %v, %v", missing, exists, err)
	}
}
