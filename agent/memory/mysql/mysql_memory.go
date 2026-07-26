package mysql

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/util"
	"github.com/torrischen/goat/util/logging"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
	gomysql "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MySQLMemory implements Memory using MySQL storage through GORM.
type MySQLMemory struct {
	db *gorm.DB
}

var _ common.Memory = (*MySQLMemory)(nil)

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
	Payload      string `gorm:"column:payload;type:longtext;not null"`
	CreatedAt    time.Time
}

func (memoryMessage) TableName() string {
	return "goat_memory_messages"
}

// NewMySQLMemory creates a MySQL-backed memory store and migrates its tables.
func NewMySQLMemory(host string, port int, username, password, dbname string) (*MySQLMemory, error) {
	dsn, err := buildDSN(host, port, username, password, dbname)
	if err != nil {
		return nil, err
	}

	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	if err := db.AutoMigrate(&memoryConversation{}, &memoryMessage{}); err != nil {
		return nil, err
	}

	return &MySQLMemory{db: db}, nil
}

func buildDSN(host string, port int, username, password, dbname string) (string, error) {
	if host == "" {
		return "", errors.New("mysql memory host is required")
	}
	if port <= 0 {
		return "", fmt.Errorf("mysql memory port must be positive: %d", port)
	}
	if username == "" {
		return "", errors.New("mysql memory username is required")
	}
	if dbname == "" {
		return "", errors.New("mysql memory dbname is required")
	}

	cfg := gomysql.NewConfig()
	cfg.User = username
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(host, strconv.Itoa(port))
	cfg.DBName = dbname
	cfg.ParseTime = true
	cfg.Params = map[string]string{
		"charset": "utf8mb4",
		"loc":     "Local",
	}

	return cfg.FormatDSN(), nil
}

func (m *MySQLMemory) InitNew(ctx context.Context) common.MemoryUID {
	memoryUID := m.NewMemoryUID(ctx)

	if err := m.ensureConversation(ctx, memoryUID); err != nil {
		logging.Errorf("Failed to initialize mysql conversation %s: %v", memoryUID, err)
	}

	return memoryUID
}

func (m *MySQLMemory) NewMemoryUID(_ context.Context) common.MemoryUID {
	return common.MemoryUID(uuid.NewString())
}

func (m *MySQLMemory) Append(ctx context.Context, memoryUID common.MemoryUID, message *schema.AgenticMessage) error {
	payload, err := encodeMessage(message)
	if err != nil {
		return err
	}

	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockConversation(tx, memoryUID); err != nil {
			return err
		}

		var messageIndex int64
		if err := tx.Model(&memoryMessage{}).
			Where("memory_uid = ?", memoryUID.String()).
			Select("COALESCE(MAX(message_index), -1) + 1").
			Scan(&messageIndex).Error; err != nil {
			return err
		}

		return tx.Create(&memoryMessage{
			MemoryUID:    memoryUID.String(),
			MessageIndex: messageIndex,
			Payload:      payload,
		}).Error
	})
}

func (m *MySQLMemory) GetAll(ctx context.Context, memoryUID common.MemoryUID) []*schema.AgenticMessage {
	var rows []memoryMessage
	if err := m.db.WithContext(ctx).
		Where("memory_uid = ?", memoryUID.String()).
		Order("message_index ASC").
		Find(&rows).Error; err != nil {
		logging.Errorf("Failed to load mysql conversation %s: %v", memoryUID, err)
		return []*schema.AgenticMessage{}
	}

	messages := make([]*schema.AgenticMessage, 0, len(rows))
	for _, row := range rows {
		msg, err := decodeMessage(row.Payload)
		if err != nil {
			logging.Errorf("Failed to decode mysql conversation %s message %d: %v", memoryUID, row.MessageIndex, err)
			continue
		}
		messages = append(messages, msg)
	}

	return common.CloneAgenticMessages(messages)
}

func (m *MySQLMemory) Len(ctx context.Context, memoryUID common.MemoryUID) int {
	var count int64
	if err := m.db.WithContext(ctx).
		Model(&memoryMessage{}).
		Where("memory_uid = ?", memoryUID.String()).
		Count(&count).Error; err != nil {
		logging.Errorf("Failed to count mysql conversation %s: %v", memoryUID, err)
		return 0
	}

	return int(count)
}

func (m *MySQLMemory) Reset(ctx context.Context, memoryUID common.MemoryUID, messages []*schema.AgenticMessage) {
	rows := make([]memoryMessage, 0, len(messages))
	for i, message := range messages {
		payload, err := encodeMessage(message)
		if err != nil {
			logging.Errorf("Failed to encode mysql conversation %s message %d: %v", memoryUID, i, err)
			return
		}
		rows = append(rows, memoryMessage{
			MemoryUID:    memoryUID.String(),
			MessageIndex: int64(i),
			Payload:      payload,
		})
	}

	if err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockConversation(tx, memoryUID); err != nil {
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
		logging.Errorf("Failed to reset mysql conversation %s: %v", memoryUID, err)
	}
}

func (m *MySQLMemory) Delete(ctx context.Context, memoryUID common.MemoryUID) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("memory_uid = ?", memoryUID.String()).Delete(&memoryMessage{}).Error; err != nil {
			return err
		}
		return tx.Where("memory_uid = ?", memoryUID.String()).Delete(&memoryConversation{}).Error
	})
}

func (m *MySQLMemory) ensureConversation(ctx context.Context, memoryUID common.MemoryUID) error {
	return m.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&memoryConversation{MemoryUID: memoryUID.String()}).
		Error
}

func lockConversation(tx *gorm.DB, memoryUID common.MemoryUID) error {
	conversation := memoryConversation{MemoryUID: memoryUID.String()}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&conversation).Error; err != nil {
		return err
	}

	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("memory_uid = ?", memoryUID.String()).
		First(&conversation).Error
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
