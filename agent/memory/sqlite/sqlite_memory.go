package sqlite

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
	"github.com/torrischen/goat/util"
	"github.com/torrischen/goat/util/logging"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultSQLitePath = "data/goat_memory.sqlite"

// SQLiteMemory implements Memory using local embedded SQLite storage through GORM.
type SQLiteMemory struct {
	db      *gorm.DB
	writeMu sync.Mutex
}

var _ common.Memory = (*SQLiteMemory)(nil)

type memoryConversation struct {
	MemoryUID string `gorm:"column:memory_uid;primaryKey;size:191"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (memoryConversation) TableName() string {
	return "goat_memory_conversations"
}

type memoryMessage struct {
	ID           uint64 `gorm:"primaryKey;autoIncrement"`
	MemoryUID    string `gorm:"column:memory_uid;size:191;not null;index:idx_goat_memory_messages_uid;uniqueIndex:idx_goat_memory_uid_message_index,priority:1"`
	MessageIndex int64  `gorm:"column:message_index;not null;uniqueIndex:idx_goat_memory_uid_message_index,priority:2"`
	Payload      string `gorm:"column:payload;type:text;not null"`
	CreatedAt    time.Time
}

func (memoryMessage) TableName() string {
	return "goat_memory_messages"
}

// NewSQLiteMemory creates a local SQLite-backed memory store and migrates its tables.
// If dbPath is empty, data/goat_memory.sqlite is used.
func NewSQLiteMemory(dbPath string) (*SQLiteMemory, error) {
	dsn, err := buildDSN(dbPath)
	if err != nil {
		return nil, err
	}

	db, err := gorm.Open(gormsqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)

	if err := configureSQLite(db); err != nil {
		return nil, err
	}

	if err := db.AutoMigrate(&memoryConversation{}, &memoryMessage{}); err != nil {
		return nil, err
	}

	return &SQLiteMemory{db: db}, nil
}

func buildDSN(dbPath string) (string, error) {
	if dbPath == "" {
		dbPath = defaultSQLitePath
	}
	if strings.TrimSpace(dbPath) == "" {
		return "", errors.New("sqlite memory db path is required")
	}

	if shouldCreateParentDir(dbPath) {
		dir := filepath.Dir(dbPath)
		if dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return "", fmt.Errorf("create sqlite memory directory: %w", err)
			}
		}
	}

	return dbPath, nil
}

func shouldCreateParentDir(dbPath string) bool {
	return dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file:")
}

func configureSQLite(db *gorm.DB) error {
	if err := db.Exec("PRAGMA busy_timeout = 5000").Error; err != nil {
		return err
	}
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		return err
	}
	return db.Exec("PRAGMA journal_mode = WAL").Error
}

func (m *SQLiteMemory) InitNew(ctx context.Context) common.MemoryUID {
	memoryUID := m.NewMemoryUID(ctx)

	if err := m.ensureConversation(ctx, memoryUID); err != nil {
		logging.Errorf("Failed to initialize sqlite conversation %s: %v", memoryUID, err)
	}

	return memoryUID
}

func (m *SQLiteMemory) NewMemoryUID(_ context.Context) common.MemoryUID {
	return common.MemoryUID(uuid.NewString())
}

func (m *SQLiteMemory) Append(ctx context.Context, memoryUID common.MemoryUID, message *schema.AgenticMessage) error {
	payload, err := encodeMessage(message)
	if err != nil {
		return err
	}

	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureConversationTx(tx, memoryUID); err != nil {
			return err
		}

		return tx.Exec(
			`INSERT INTO goat_memory_messages (memory_uid, message_index, payload, created_at)
SELECT ?, COALESCE(MAX(message_index), -1) + 1, ?, ?
FROM goat_memory_messages
WHERE memory_uid = ?`,
			memoryUID.String(),
			payload,
			time.Now(),
			memoryUID.String(),
		).Error
	})
}

func (m *SQLiteMemory) GetAll(ctx context.Context, memoryUID common.MemoryUID) []*schema.AgenticMessage {
	var rows []memoryMessage
	if err := m.db.WithContext(ctx).
		Where("memory_uid = ?", memoryUID.String()).
		Order("message_index ASC").
		Find(&rows).Error; err != nil {
		logging.Errorf("Failed to load sqlite conversation %s: %v", memoryUID, err)
		return []*schema.AgenticMessage{}
	}

	messages := make([]*schema.AgenticMessage, 0, len(rows))
	for _, row := range rows {
		msg, err := decodeMessage(row.Payload)
		if err != nil {
			logging.Errorf("Failed to decode sqlite conversation %s message %d: %v", memoryUID, row.MessageIndex, err)
			continue
		}
		messages = append(messages, msg)
	}

	return common.CloneAgenticMessages(messages)
}

func (m *SQLiteMemory) Len(ctx context.Context, memoryUID common.MemoryUID) int {
	var count int64
	if err := m.db.WithContext(ctx).
		Model(&memoryMessage{}).
		Where("memory_uid = ?", memoryUID.String()).
		Count(&count).Error; err != nil {
		logging.Errorf("Failed to count sqlite conversation %s: %v", memoryUID, err)
		return 0
	}

	return int(count)
}

func (m *SQLiteMemory) Reset(ctx context.Context, memoryUID common.MemoryUID, messages []*schema.AgenticMessage) {
	rows := make([]memoryMessage, 0, len(messages))
	for i, message := range messages {
		payload, err := encodeMessage(message)
		if err != nil {
			logging.Errorf("Failed to encode sqlite conversation %s message %d: %v", memoryUID, i, err)
			return
		}
		rows = append(rows, memoryMessage{
			MemoryUID:    memoryUID.String(),
			MessageIndex: int64(i),
			Payload:      payload,
		})
	}

	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	if err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureConversationTx(tx, memoryUID); err != nil {
			return err
		}
		if err := tx.Where("memory_uid = ?", memoryUID.String()).Delete(&memoryMessage{}).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	}); err != nil {
		logging.Errorf("Failed to reset sqlite conversation %s: %v", memoryUID, err)
	}
}

func (m *SQLiteMemory) Delete(ctx context.Context, memoryUID common.MemoryUID) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("memory_uid = ?", memoryUID.String()).Delete(&memoryMessage{}).Error; err != nil {
			return err
		}
		return tx.Where("memory_uid = ?", memoryUID.String()).Delete(&memoryConversation{}).Error
	})
}

func (m *SQLiteMemory) ensureConversation(ctx context.Context, memoryUID common.MemoryUID) error {
	return ensureConversationTx(m.db.WithContext(ctx), memoryUID)
}

func ensureConversationTx(tx *gorm.DB, memoryUID common.MemoryUID) error {
	return tx.
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&memoryConversation{MemoryUID: memoryUID.String()}).
		Error
}

func encodeMessage(message *schema.AgenticMessage) (string, error) {
	b, err := sonic.Marshal(message)
	if err != nil {
		return "", err
	}
	return util.ByteToString(b), nil
}

func decodeMessage(payload string) (*schema.AgenticMessage, error) {
	var message schema.AgenticMessage
	if err := sonic.UnmarshalString(payload, &message); err != nil {
		return nil, err
	}
	return &message, nil
}
