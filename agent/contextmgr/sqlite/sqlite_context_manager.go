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
	"github.com/torrischen/goat/agent/contextmgr"
	"github.com/torrischen/goat/util"
	"github.com/torrischen/goat/util/logging"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultSQLitePath = "data/goat_context.sqlite"

// SQLiteContextManager manages conversation context using local embedded SQLite through GORM.
type SQLiteContextManager struct {
	db      *gorm.DB
	writeMu sync.Mutex
}

var _ contextmgr.ContextManager = (*SQLiteContextManager)(nil)

type contextConversation struct {
	ContextUID string `gorm:"column:context_uid;primaryKey;size:191"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (contextConversation) TableName() string {
	return "goat_context_conversations"
}

type contextMessage struct {
	ID           uint64 `gorm:"primaryKey;autoIncrement"`
	ContextUID   string `gorm:"column:context_uid;size:191;not null;index:idx_goat_context_messages_uid;uniqueIndex:idx_goat_context_uid_message_index,priority:1"`
	MessageIndex int64  `gorm:"column:message_index;not null;uniqueIndex:idx_goat_context_uid_message_index,priority:2"`
	Payload      string `gorm:"column:payload;type:text;not null"`
	CreatedAt    time.Time
}

func (contextMessage) TableName() string {
	return "goat_context_messages"
}

// NewSQLiteContextManager creates a local SQLite-backed context manager and migrates its tables.
// If dbPath is empty, data/goat_context.sqlite is used.
func NewSQLiteContextManager(dbPath string) (*SQLiteContextManager, error) {
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

	if err := db.AutoMigrate(&contextConversation{}, &contextMessage{}); err != nil {
		return nil, err
	}

	return &SQLiteContextManager{db: db}, nil
}

func buildDSN(dbPath string) (string, error) {
	if dbPath == "" {
		dbPath = defaultSQLitePath
	}
	if strings.TrimSpace(dbPath) == "" {
		return "", errors.New("sqlite context manager db path is required")
	}

	if shouldCreateParentDir(dbPath) {
		dir := filepath.Dir(dbPath)
		if dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return "", fmt.Errorf("create sqlite context manager directory: %w", err)
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

func (m *SQLiteContextManager) InitNew(ctx context.Context) common.ContextUID {
	contextUID := m.NewContextUID(ctx)

	if err := m.ensureConversation(ctx, contextUID); err != nil {
		logging.Errorf("Failed to initialize sqlite conversation %s: %v", contextUID, err)
	}

	return contextUID
}

func (m *SQLiteContextManager) NewContextUID(_ context.Context) common.ContextUID {
	return common.ContextUID(uuid.NewString())
}

func (m *SQLiteContextManager) Append(ctx context.Context, contextUID common.ContextUID, message *schema.AgenticMessage) error {
	payload, err := encodeMessage(message)
	if err != nil {
		return err
	}

	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureConversationTx(tx, contextUID); err != nil {
			return err
		}

		return tx.Exec(
			`INSERT INTO goat_context_messages (context_uid, message_index, payload, created_at)
SELECT ?, COALESCE(MAX(message_index), -1) + 1, ?, ?
FROM goat_context_messages
WHERE context_uid = ?`,
			contextUID.String(),
			payload,
			time.Now(),
			contextUID.String(),
		).Error
	})
}

func (m *SQLiteContextManager) GetAll(ctx context.Context, contextUID common.ContextUID) []*schema.AgenticMessage {
	var rows []contextMessage
	if err := m.db.WithContext(ctx).
		Where("context_uid = ?", contextUID.String()).
		Order("message_index ASC").
		Find(&rows).Error; err != nil {
		logging.Errorf("Failed to load sqlite conversation %s: %v", contextUID, err)
		return []*schema.AgenticMessage{}
	}

	messages := make([]*schema.AgenticMessage, 0, len(rows))
	for _, row := range rows {
		msg, err := decodeMessage(row.Payload)
		if err != nil {
			logging.Errorf("Failed to decode sqlite conversation %s message %d: %v", contextUID, row.MessageIndex, err)
			continue
		}
		messages = append(messages, msg)
	}

	return common.CloneAgenticMessages(messages)
}

func (m *SQLiteContextManager) Len(ctx context.Context, contextUID common.ContextUID) int {
	var count int64
	if err := m.db.WithContext(ctx).
		Model(&contextMessage{}).
		Where("context_uid = ?", contextUID.String()).
		Count(&count).Error; err != nil {
		logging.Errorf("Failed to count sqlite conversation %s: %v", contextUID, err)
		return 0
	}

	return int(count)
}

func (m *SQLiteContextManager) Reset(ctx context.Context, contextUID common.ContextUID, messages []*schema.AgenticMessage) {
	rows := make([]contextMessage, 0, len(messages))
	for i, message := range messages {
		payload, err := encodeMessage(message)
		if err != nil {
			logging.Errorf("Failed to encode sqlite conversation %s message %d: %v", contextUID, i, err)
			return
		}
		rows = append(rows, contextMessage{
			ContextUID:   contextUID.String(),
			MessageIndex: int64(i),
			Payload:      payload,
		})
	}

	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	if err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureConversationTx(tx, contextUID); err != nil {
			return err
		}
		if err := tx.Where("context_uid = ?", contextUID.String()).Delete(&contextMessage{}).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	}); err != nil {
		logging.Errorf("Failed to reset sqlite conversation %s: %v", contextUID, err)
	}
}

func (m *SQLiteContextManager) Delete(ctx context.Context, contextUID common.ContextUID) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("context_uid = ?", contextUID.String()).Delete(&contextMessage{}).Error; err != nil {
			return err
		}
		return tx.Where("context_uid = ?", contextUID.String()).Delete(&contextConversation{}).Error
	})
}

func (m *SQLiteContextManager) ensureConversation(ctx context.Context, contextUID common.ContextUID) error {
	return ensureConversationTx(m.db.WithContext(ctx), contextUID)
}

func ensureConversationTx(tx *gorm.DB, contextUID common.ContextUID) error {
	return tx.
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&contextConversation{ContextUID: contextUID.String()}).
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
