package contextmgr_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/contextmgr"
	filectx "github.com/torrischen/goat/agent/contextmgr/file"
	"github.com/torrischen/goat/agent/contextmgr/ram"
	"github.com/torrischen/goat/agent/contextmgr/sqlite"

	"github.com/cloudwego/eino/schema"
)

func TestContextManagerPendingInboxContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		new  func(*testing.T) contextmgr.ContextManager
	}{
		{
			name: "ram",
			new: func(*testing.T) contextmgr.ContextManager {
				return ram.NewRAMContextManager()
			},
		},
		{
			name: "file",
			new: func(t *testing.T) contextmgr.ContextManager {
				return filectx.NewFileContextManager(t.TempDir())
			},
		},
		{
			name: "sqlite",
			new: func(t *testing.T) contextmgr.ContextManager {
				manager, err := sqlite.NewSQLiteContextManager(filepath.Join(t.TempDir(), "context.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				return manager
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			testPendingInboxContract(t, test.new(t))
		})
	}
}

func TestContextManagersPreserveRunBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		new  func(*testing.T) contextmgr.ContextManager
	}{
		{name: "ram", new: func(*testing.T) contextmgr.ContextManager { return ram.NewRAMContextManager() }},
		{name: "file", new: func(t *testing.T) contextmgr.ContextManager {
			return filectx.NewFileContextManager(t.TempDir())
		}},
		{name: "sqlite", new: func(t *testing.T) contextmgr.ContextManager {
			manager, err := sqlite.NewSQLiteContextManager(filepath.Join(t.TempDir(), "context.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			return manager
		}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manager := test.new(t)
			ctx := context.Background()
			contextUID := manager.InitNew(ctx)
			message := schema.UserAgenticMessage("request")
			common.MarkRunStart(message, "run")
			if err := manager.Append(ctx, contextUID, message); err != nil {
				t.Fatal(err)
			}

			stored := manager.GetAll(ctx, contextUID)
			if len(stored) != 1 {
				t.Fatalf("stored message count = %d, want 1", len(stored))
			}
			if got, ok := common.RunUIDFromMessage(stored[0]); !ok || got != "run" {
				t.Fatalf("stored run UID = %q, %v", got, ok)
			}
		})
	}
}

func testPendingInboxContract(t *testing.T, manager contextmgr.ContextManager) {
	t.Helper()

	ctx := context.Background()
	contextUID := manager.InitNew(ctx)
	if err := manager.Append(ctx, contextUID, schema.SystemAgenticMessage("system")); err != nil {
		t.Fatalf("Append system message: %v", err)
	}

	steering := []*schema.AgenticMessage{
		schema.UserAgenticMessage("steer one"),
		schema.UserAgenticMessage("steer two"),
	}
	if err := manager.EnqueuePendingMessages(ctx, contextUID, steering); err != nil {
		t.Fatalf("EnqueuePendingMessages: %v", err)
	}

	if got := manager.GetAll(ctx, contextUID); len(got) != 1 {
		t.Fatalf("pending messages became visible before CommitTurn: len(GetAll()) = %d", len(got))
	}

	// Reset must replace only committed history and preserve the pending inbox.
	manager.Reset(ctx, contextUID, []*schema.AgenticMessage{schema.SystemAgenticMessage("reset system")})
	turn := common.AssistantTextMessage("assistant turn")
	result, err := manager.CommitTurn(ctx, contextUID, []*schema.AgenticMessage{turn})
	if err != nil {
		t.Fatalf("CommitTurn: %v", err)
	}
	assertTexts(t, result.AppliedPendingMessages, []string{"steer one", "steer two"})
	assertTexts(t, manager.GetAll(ctx, contextUID), []string{
		"reset system",
		"assistant turn",
		"steer one",
		"steer two",
	})

	result, err = manager.CommitTurn(ctx, contextUID, []*schema.AgenticMessage{schema.UserAgenticMessage("next boundary")})
	if err != nil {
		t.Fatalf("second CommitTurn: %v", err)
	}
	if len(result.AppliedPendingMessages) != 0 {
		t.Fatalf("pending messages were applied more than once: %d", len(result.AppliedPendingMessages))
	}

	if err := manager.EnqueuePendingMessages(ctx, contextUID, []*schema.AgenticMessage{common.AssistantTextMessage("invalid")}); !errors.Is(err, contextmgr.ErrInvalidPendingMessage) {
		t.Fatalf("EnqueuePendingMessages(non-user) error = %v, want ErrInvalidPendingMessage", err)
	}

	// A final answer wins the boundary and atomically discards queued steering.
	if err := manager.EnqueuePendingMessages(ctx, contextUID, []*schema.AgenticMessage{
		schema.UserAgenticMessage("discarded steer"),
	}); err != nil {
		t.Fatalf("enqueue before final: %v", err)
	}
	if err := manager.CommitFinal(ctx, contextUID, common.AssistantTextMessage("final answer")); err != nil {
		t.Fatalf("CommitFinal: %v", err)
	}
	assertTexts(t, manager.GetAll(ctx, contextUID), []string{
		"reset system",
		"assistant turn",
		"steer one",
		"steer two",
		"next boundary",
		"final answer",
	})
	if err := manager.EnqueuePendingMessages(ctx, contextUID, steering); !errors.Is(err, contextmgr.ErrConversationFinalized) {
		t.Fatalf("EnqueuePendingMessages(after final) error = %v, want ErrConversationFinalized", err)
	}

	// Appending the next Do user input reopens steering without a run registry.
	if err := manager.Append(ctx, contextUID, schema.UserAgenticMessage("new run")); err != nil {
		t.Fatalf("Append new run input: %v", err)
	}
	if err := manager.EnqueuePendingMessages(ctx, contextUID, steering); err != nil {
		t.Fatalf("EnqueuePendingMessages(after new user input): %v", err)
	}

	unknown := manager.NewContextUID(ctx)
	if err := manager.EnqueuePendingMessages(ctx, unknown, steering); !errors.Is(err, contextmgr.ErrContextNotFound) {
		t.Fatalf("EnqueuePendingMessages(unknown) error = %v, want ErrContextNotFound", err)
	}
}

func TestFileContextManagerPersistsPendingInbox(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	first := filectx.NewFileContextManager(dir)
	contextUID := first.InitNew(ctx)
	if err := first.EnqueuePendingMessages(ctx, contextUID, []*schema.AgenticMessage{
		schema.UserAgenticMessage("persisted steer"),
	}); err != nil {
		t.Fatal(err)
	}

	// Recreate the manager to force a disk read rather than a cache hit.
	second := filectx.NewFileContextManager(dir)
	result, err := second.CommitTurn(ctx, contextUID, []*schema.AgenticMessage{
		common.AssistantTextMessage("turn"),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertTexts(t, result.AppliedPendingMessages, []string{"persisted steer"})
	assertTexts(t, second.GetAll(ctx, contextUID), []string{"turn", "persisted steer"})
}

func assertTexts(t *testing.T, messages []*schema.AgenticMessage, want []string) {
	t.Helper()
	if len(messages) != len(want) {
		t.Fatalf("message count = %d, want %d", len(messages), len(want))
	}
	for i, message := range messages {
		if got := messageText(message); got != want[i] {
			t.Fatalf("message[%d] text = %q, want %q", i, got, want[i])
		}
	}
}

func messageText(message *schema.AgenticMessage) string {
	if message == nil {
		return ""
	}
	for _, block := range message.ContentBlocks {
		if block == nil {
			continue
		}
		if block.UserInputText != nil {
			return block.UserInputText.Text
		}
		if block.AssistantGenText != nil {
			return block.AssistantGenText.Text
		}
	}
	return ""
}
