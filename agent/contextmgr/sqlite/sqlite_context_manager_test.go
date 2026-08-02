package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/contextmgr"
	"github.com/torrischen/goat/util"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSQLiteStoreMigratesV02Schema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := gorm.Open(gormsqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE goat_context_conversations (
context_uid text PRIMARY KEY,
created_at datetime,
updated_at datetime
)`,
		`CREATE TABLE goat_context_messages (
id integer PRIMARY KEY AUTOINCREMENT,
context_uid text NOT NULL,
message_index integer NOT NULL,
payload text NOT NULL,
created_at datetime
)`,
		`CREATE UNIQUE INDEX idx_goat_context_uid_message_index
ON goat_context_messages(context_uid, message_index)`,
		`INSERT INTO goat_context_conversations(context_uid) VALUES ('legacy')`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	messagePayload := mustEncode(t, schema.UserAgenticMessage("legacy message"))
	if err := db.Exec(
		`INSERT INTO goat_context_messages(context_uid, message_index, payload)
VALUES (?, 0, ?)`,
		"legacy",
		messagePayload,
	).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(context.Background(), "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != 1 || len(state.Messages) != 1 {
		t.Fatalf("migrated state = %+v", state)
	}
}

func TestSQLiteStoreMigratesLegacyContextOnFirstCAS(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "context.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	contextUID := common.ContextUID("legacy-context")
	runUID := common.RunUID("legacy-run")
	user := schema.UserAgenticMessage("request")
	common.MarkRunStart(user, runUID)
	messages := []*schema.AgenticMessage{schema.SystemAgenticMessage("system"), user}

	if err := store.db.WithContext(ctx).Create(&contextConversation{
		ContextUID: contextUID.String(),
		Revision:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for i, message := range messages {
		payload := mustEncode(t, message)
		if err := store.db.WithContext(ctx).Create(&contextMessage{
			ContextUID:   contextUID.String(),
			MessageIndex: int64(i),
			Payload:      payload,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.db.WithContext(ctx).Create(&pendingMessage{
		ContextUID: contextUID.String(),
		Payload:    mustEncode(t, schema.UserAgenticMessage("pending")),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.WithContext(ctx).Create(&runSnapshot{
		ContextUID: contextUID.String(),
		RunUID:     runUID.String(),
		Payload:    mustEncode(t, messages),
	}).Error; err != nil {
		t.Fatal(err)
	}

	state, err := store.Load(ctx, contextUID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Messages) != 2 || len(state.PendingMessages) != 1 || len(state.RunSnapshots) != 1 {
		t.Fatalf("legacy state = %+v", state)
	}

	manager := contextmgr.NewManager(store)
	forkedUID, err := manager.Fork(ctx, common.RunSignature{ContextUID: contextUID, RunUID: runUID})
	if err != nil || forkedUID == "" {
		t.Fatalf("Fork(legacy snapshot) = %q, %v", forkedUID, err)
	}
	result, err := manager.CommitTurn(ctx, contextUID, nil)
	if err != nil || len(result.AppliedPendingMessages) != 1 {
		t.Fatalf("CommitTurn() = %+v, %v", result, err)
	}

	var row contextConversation
	if err := store.db.WithContext(ctx).Where("context_uid = ?", contextUID.String()).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Revision != 2 || row.StatePayload == "" {
		t.Fatalf("migrated row = %+v", row)
	}
}

func TestNewSQLiteContextManager(t *testing.T) {
	manager, err := NewSQLiteContextManager(filepath.Join(t.TempDir(), "context.sqlite"))
	if err != nil || manager == nil {
		t.Fatalf("NewSQLiteContextManager() = %v, %v", manager, err)
	}
}

func mustEncode(t *testing.T, value any) string {
	t.Helper()
	payload, err := sonic.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return util.ByteToString(payload)
}
