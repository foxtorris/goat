package mysql

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/torrischen/goat/agent/common"
)

func TestBuildDSN(t *testing.T) {
	for _, test := range []struct {
		host, user, database string
		port                 int
	}{
		{"", "user", "db", 3306},
		{"localhost", "user", "db", 0},
		{"localhost", "", "db", 3306},
		{"localhost", "user", "", 3306},
	} {
		if _, err := buildDSN(test.host, test.port, test.user, "pass", test.database); err == nil {
			t.Fatalf("buildDSN(%q, %d, %q, db=%q) succeeded", test.host, test.port, test.user, test.database)
		}
	}
	dsn, err := buildDSN("::1", 3306, "user", "p@ss", "database")
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"user:p@ss@tcp([::1]:3306)/database", "charset=utf8mb4", "parseTime=true"} {
		if !strings.Contains(dsn, part) {
			t.Fatalf("DSN %q does not contain %q", dsn, part)
		}
	}
}

func TestModelsAndMessageCodec(t *testing.T) {
	if (contextConversation{}).TableName() != "goat_context_conversations" ||
		(contextMessage{}).TableName() != "goat_context_messages" ||
		(pendingMessage{}).TableName() != "goat_context_pending_messages" {
		t.Fatal("unexpected table names")
	}
	message := common.AssistantTextMessage("hello")
	payload, err := encodeMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeMessage(payload)
	if err != nil || decoded.Role != schema.AgenticRoleTypeAssistant || decoded.ContentBlocks[0].AssistantGenText.Text != "hello" {
		t.Fatalf("decoded = %+v, %v", decoded, err)
	}
	if _, err := decodeMessage("not-json"); err == nil {
		t.Fatal("invalid payload decoded")
	}
	m := &MysqlContextManager{}
	first := m.NewContextUID(context.Background())
	second := m.NewContextUID(context.Background())
	if first == "" || first == second {
		t.Fatalf("generated IDs = %q, %q", first, second)
	}
}

func TestNewMysqlContextManagerRejectsInvalidConfiguration(t *testing.T) {
	if _, err := NewMysqlContextManager("", 3306, "user", "pass", "db"); err == nil {
		t.Fatal("invalid configuration accepted")
	}
}
