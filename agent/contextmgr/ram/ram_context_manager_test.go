package ram

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/contextmgr"
)

func TestRAMContextManagerLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewRAMContextManager()
	id := m.InitNew(ctx)
	if id == "" || m.NewContextUID(ctx) == id || m.Len(ctx, id) != 0 {
		t.Fatal("context initialization failed")
	}
	user := schema.UserAgenticMessage("hello")
	if err := m.Append(ctx, id, user); err != nil || m.Len(ctx, id) != 1 {
		t.Fatalf("Append = %v, len %d", err, m.Len(ctx, id))
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
	pending[0] = nil
	m.Reset(ctx, id, []*schema.AgenticMessage{schema.SystemAgenticMessage("system")})
	result, err := m.CommitTurn(ctx, id, []*schema.AgenticMessage{common.AssistantTextMessage("turn")})
	if err != nil || len(result.AppliedPendingMessages) != 1 || m.Len(ctx, id) != 3 {
		t.Fatalf("CommitTurn = %+v, %v, len %d", result, err, m.Len(ctx, id))
	}
	result.AppliedPendingMessages[0] = nil
	if m.GetAll(ctx, id)[2] == nil {
		t.Fatal("commit result aliases history")
	}
	if err := m.EnqueuePendingMessages(ctx, id, []*schema.AgenticMessage{common.AssistantTextMessage("bad")}); !errors.Is(err, contextmgr.ErrInvalidPendingMessage) {
		t.Fatalf("invalid pending error = %v", err)
	}
	missing := common.ContextUID("missing")
	if err := m.EnqueuePendingMessages(ctx, missing, []*schema.AgenticMessage{schema.UserAgenticMessage("x")}); !errors.Is(err, contextmgr.ErrContextNotFound) {
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
	if err := m.EnqueuePendingMessages(ctx, id, []*schema.AgenticMessage{schema.UserAgenticMessage("discard")}); err != nil {
		t.Fatal(err)
	}
	if err := m.CommitFinal(ctx, id, common.AssistantTextMessage("final")); err != nil {
		t.Fatal(err)
	}
	if err := m.EnqueuePendingMessages(ctx, id, []*schema.AgenticMessage{schema.UserAgenticMessage("late")}); !errors.Is(err, contextmgr.ErrConversationFinalized) {
		t.Fatalf("enqueue after final = %v", err)
	}
	if err := m.Delete(ctx, id); err != nil || m.Len(ctx, id) != 0 {
		t.Fatalf("Delete = %v, len %d", err, m.Len(ctx, id))
	}
}
