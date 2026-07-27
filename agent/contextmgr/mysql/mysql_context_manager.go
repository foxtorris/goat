package mysql

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/torrischen/goat/agent/common"
	"github.com/torrischen/goat/agent/contextmgr"
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

// MysqlContextManager manages conversation context using MySQL through GORM.
type MysqlContextManager struct {
	db *gorm.DB
}

var _ contextmgr.ContextManager = (*MysqlContextManager)(nil)

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
	Payload      string `gorm:"column:payload;type:longtext;not null"`
	CreatedAt    time.Time
}

func (contextMessage) TableName() string {
	return "goat_context_messages"
}

type pendingMessage struct {
	ID         uint64 `gorm:"primaryKey;autoIncrement"`
	ContextUID string `gorm:"column:context_uid;size:191;not null;index:idx_goat_context_pending_messages_uid"`
	Payload    string `gorm:"column:payload;type:longtext;not null"`
	CreatedAt  time.Time
}

func (pendingMessage) TableName() string {
	return "goat_context_pending_messages"
}

// NewMysqlContextManager creates a MySQL-backed context manager and migrates its tables.
func NewMysqlContextManager(host string, port int, username, password, dbname string) (*MysqlContextManager, error) {
	dsn, err := buildDSN(host, port, username, password, dbname)
	if err != nil {
		return nil, err
	}

	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	if err := db.AutoMigrate(&contextConversation{}, &contextMessage{}, &pendingMessage{}); err != nil {
		return nil, err
	}

	return &MysqlContextManager{db: db}, nil
}

func buildDSN(host string, port int, username, password, dbname string) (string, error) {
	if host == "" {
		return "", errors.New("mysql context manager host is required")
	}
	if port <= 0 {
		return "", fmt.Errorf("mysql context manager port must be positive: %d", port)
	}
	if username == "" {
		return "", errors.New("mysql context manager username is required")
	}
	if dbname == "" {
		return "", errors.New("mysql context manager dbname is required")
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

func (m *MysqlContextManager) InitNew(ctx context.Context) common.ContextUID {
	contextUID := m.NewContextUID(ctx)

	if err := m.ensureConversation(ctx, contextUID); err != nil {
		logging.Errorf("Failed to initialize mysql conversation %s: %v", contextUID, err)
	}

	return contextUID
}

func (m *MysqlContextManager) NewContextUID(_ context.Context) common.ContextUID {
	return common.ContextUID(uuid.NewString())
}

func (m *MysqlContextManager) Append(ctx context.Context, contextUID common.ContextUID, message *schema.AgenticMessage) error {
	payload, err := encodeMessage(message)
	if err != nil {
		return err
	}

	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockConversation(tx, contextUID); err != nil {
			return err
		}

		var messageIndex int64
		if err := tx.Model(&contextMessage{}).
			Where("context_uid = ?", contextUID.String()).
			Select("COALESCE(MAX(message_index), -1) + 1").
			Scan(&messageIndex).Error; err != nil {
			return err
		}

		return tx.Create(&contextMessage{
			ContextUID:   contextUID.String(),
			MessageIndex: messageIndex,
			Payload:      payload,
		}).Error
	})
}

func (m *MysqlContextManager) GetAll(ctx context.Context, contextUID common.ContextUID) []*schema.AgenticMessage {
	var rows []contextMessage
	if err := m.db.WithContext(ctx).
		Where("context_uid = ?", contextUID.String()).
		Order("message_index ASC").
		Find(&rows).Error; err != nil {
		logging.Errorf("Failed to load mysql conversation %s: %v", contextUID, err)
		return []*schema.AgenticMessage{}
	}

	messages := make([]*schema.AgenticMessage, 0, len(rows))
	for _, row := range rows {
		msg, err := decodeMessage(row.Payload)
		if err != nil {
			logging.Errorf("Failed to decode mysql conversation %s message %d: %v", contextUID, row.MessageIndex, err)
			continue
		}
		messages = append(messages, msg)
	}

	return common.CloneAgenticMessages(messages)
}

func (m *MysqlContextManager) Len(ctx context.Context, contextUID common.ContextUID) int {
	var count int64
	if err := m.db.WithContext(ctx).
		Model(&contextMessage{}).
		Where("context_uid = ?", contextUID.String()).
		Count(&count).Error; err != nil {
		logging.Errorf("Failed to count mysql conversation %s: %v", contextUID, err)
		return 0
	}

	return int(count)
}

func (m *MysqlContextManager) Reset(ctx context.Context, contextUID common.ContextUID, messages []*schema.AgenticMessage) {
	rows := make([]contextMessage, 0, len(messages))
	for i, message := range messages {
		payload, err := encodeMessage(message)
		if err != nil {
			logging.Errorf("Failed to encode mysql conversation %s message %d: %v", contextUID, i, err)
			return
		}
		rows = append(rows, contextMessage{
			ContextUID:   contextUID.String(),
			MessageIndex: int64(i),
			Payload:      payload,
		})
	}

	if err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockConversation(tx, contextUID); err != nil {
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
		logging.Errorf("Failed to reset mysql conversation %s: %v", contextUID, err)
	}
}

func (m *MysqlContextManager) EnqueuePendingMessages(
	ctx context.Context,
	contextUID common.ContextUID,
	messages []*schema.AgenticMessage,
) error {
	if err := contextmgr.ValidatePendingMessages(messages); err != nil {
		return err
	}

	rows := make([]pendingMessage, 0, len(messages))
	for _, message := range messages {
		payload, err := encodeMessage(message)
		if err != nil {
			return err
		}
		rows = append(rows, pendingMessage{ContextUID: contextUID.String(), Payload: payload})
	}

	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockExistingConversation(tx, contextUID); err != nil {
			return err
		}
		finalized, err := conversationFinalizedTx(tx, contextUID)
		if err != nil {
			return err
		}
		if finalized {
			return contextmgr.ErrConversationFinalized
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
}

func (m *MysqlContextManager) CommitTurn(
	ctx context.Context,
	contextUID common.ContextUID,
	turnMessages []*schema.AgenticMessage,
) (*contextmgr.TurnCommitResult, error) {
	turnPayloads := make([]string, 0, len(turnMessages))
	for _, message := range turnMessages {
		payload, err := encodeMessage(message)
		if err != nil {
			return nil, err
		}
		turnPayloads = append(turnPayloads, payload)
	}

	applied := make([]*schema.AgenticMessage, 0)
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockExistingConversation(tx, contextUID); err != nil {
			return err
		}

		var pendingRows []pendingMessage
		if err := tx.Where("context_uid = ?", contextUID.String()).
			Order("id ASC").
			Find(&pendingRows).Error; err != nil {
			return err
		}

		applied = make([]*schema.AgenticMessage, 0, len(pendingRows))
		for _, row := range pendingRows {
			message, err := decodeMessage(row.Payload)
			if err != nil {
				return err
			}
			applied = append(applied, message)
		}

		var nextIndex int64
		if err := tx.Model(&contextMessage{}).
			Where("context_uid = ?", contextUID.String()).
			Select("COALESCE(MAX(message_index), -1) + 1").
			Scan(&nextIndex).Error; err != nil {
			return err
		}

		rows := make([]contextMessage, 0, len(turnPayloads)+len(pendingRows))
		for _, payload := range turnPayloads {
			rows = append(rows, contextMessage{
				ContextUID:   contextUID.String(),
				MessageIndex: nextIndex,
				Payload:      payload,
			})
			nextIndex++
		}
		for _, row := range pendingRows {
			rows = append(rows, contextMessage{
				ContextUID:   contextUID.String(),
				MessageIndex: nextIndex,
				Payload:      row.Payload,
			})
			nextIndex++
		}
		if len(rows) > 0 {
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		if len(pendingRows) > 0 {
			if err := tx.Where("context_uid = ?", contextUID.String()).Delete(&pendingMessage{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &contextmgr.TurnCommitResult{
		AppliedPendingMessages: common.CloneAgenticMessages(applied),
	}, nil
}

func (m *MysqlContextManager) CommitFinal(
	ctx context.Context,
	contextUID common.ContextUID,
	message *schema.AgenticMessage,
) error {
	if err := contextmgr.ValidateFinalMessage(message); err != nil {
		return err
	}
	payload, err := encodeMessage(message)
	if err != nil {
		return err
	}

	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockExistingConversation(tx, contextUID); err != nil {
			return err
		}

		var nextIndex int64
		if err := tx.Model(&contextMessage{}).
			Where("context_uid = ?", contextUID.String()).
			Select("COALESCE(MAX(message_index), -1) + 1").
			Scan(&nextIndex).Error; err != nil {
			return err
		}
		if err := tx.Create(&contextMessage{
			ContextUID:   contextUID.String(),
			MessageIndex: nextIndex,
			Payload:      payload,
		}).Error; err != nil {
			return err
		}
		return tx.Where("context_uid = ?", contextUID.String()).Delete(&pendingMessage{}).Error
	})
}

func (m *MysqlContextManager) Delete(ctx context.Context, contextUID common.ContextUID) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("context_uid = ?", contextUID.String()).Delete(&pendingMessage{}).Error; err != nil {
			return err
		}
		if err := tx.Where("context_uid = ?", contextUID.String()).Delete(&contextMessage{}).Error; err != nil {
			return err
		}
		return tx.Where("context_uid = ?", contextUID.String()).Delete(&contextConversation{}).Error
	})
}

func (m *MysqlContextManager) ensureConversation(ctx context.Context, contextUID common.ContextUID) error {
	return m.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&contextConversation{ContextUID: contextUID.String()}).
		Error
}

func lockConversation(tx *gorm.DB, contextUID common.ContextUID) error {
	conversation := contextConversation{ContextUID: contextUID.String()}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&conversation).Error; err != nil {
		return err
	}

	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("context_uid = ?", contextUID.String()).
		First(&conversation).Error
}

func lockExistingConversation(tx *gorm.DB, contextUID common.ContextUID) error {
	conversation := contextConversation{}
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("context_uid = ?", contextUID.String()).
		First(&conversation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return contextmgr.ErrContextNotFound
	}
	return err
}

func conversationFinalizedTx(tx *gorm.DB, contextUID common.ContextUID) (bool, error) {
	var row contextMessage
	err := tx.Where("context_uid = ?", contextUID.String()).
		Order("message_index DESC").
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	message, err := decodeMessage(row.Payload)
	if err != nil {
		return false, err
	}
	return contextmgr.IsFinalAnswerMessage(message), nil
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
