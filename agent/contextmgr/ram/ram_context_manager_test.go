package ram

import (
	"context"
	"errors"
	"testing"

	"github.com/torrischen/goat/agent/contextmgr"

	"github.com/cloudwego/eino/schema"
)

func TestRAMStoreHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := NewRAMStore()
	if _, err := store.Create(ctx, contextmgr.NewState(nil)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestNewRAMContextManager(t *testing.T) {
	manager := NewRAMContextManager()
	contextUID, err := manager.Create(context.Background(), []*schema.AgenticMessage{
		schema.UserAgenticMessage("hello"),
	})
	if err != nil || contextUID == "" {
		t.Fatalf("Create() = %q, %v", contextUID, err)
	}
}
