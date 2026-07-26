package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/torrischen/goat/agent/common"

	"github.com/cloudwego/eino/schema"
)

func TestSQLiteContextManagerLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	manager, err := NewSQLiteContextManager(filepath.Join(t.TempDir(), "context.sqlite"))
	if err != nil {
		t.Fatal(err)
	}

	contextUID := manager.InitNew(ctx)
	if got := manager.Len(ctx, contextUID); got != 0 {
		t.Fatalf("Len() after InitNew = %d, want 0", got)
	}

	userMsg := common.TextMessage(schema.AgenticRoleTypeUser, "hello")
	assistantMsg := common.AssistantTextMessage("world")
	if err := manager.Append(ctx, contextUID, userMsg); err != nil {
		t.Fatalf("Append user message: %v", err)
	}
	if err := manager.Append(ctx, contextUID, assistantMsg); err != nil {
		t.Fatalf("Append assistant message: %v", err)
	}

	if got := manager.Len(ctx, contextUID); got != 2 {
		t.Fatalf("Len() after append = %d, want 2", got)
	}
	messages := manager.GetAll(ctx, contextUID)
	if got := len(messages); got != 2 {
		t.Fatalf("len(GetAll()) = %d, want 2", got)
	}
	assertMessageEqual(t, messages[0], userMsg)
	assertMessageEqual(t, messages[1], assistantMsg)

	manager.Reset(ctx, contextUID, []*schema.AgenticMessage{
		common.TextMessage(schema.AgenticRoleTypeUser, "reset"),
	})
	if got := manager.Len(ctx, contextUID); got != 1 {
		t.Fatalf("Len() after reset = %d, want 1", got)
	}

	if err := manager.Delete(ctx, contextUID); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	if got := manager.Len(ctx, contextUID); got != 0 {
		t.Fatalf("Len() after delete = %d, want 0", got)
	}
}

func assertMessageEqual(t *testing.T, got, want *schema.AgenticMessage) {
	t.Helper()

	gotPayload, err := encodeMessage(got)
	if err != nil {
		t.Fatalf("encode got message: %v", err)
	}
	wantPayload, err := encodeMessage(want)
	if err != nil {
		t.Fatalf("encode want message: %v", err)
	}
	if gotPayload != wantPayload {
		t.Fatalf("message payload = %s, want %s", gotPayload, wantPayload)
	}
}
