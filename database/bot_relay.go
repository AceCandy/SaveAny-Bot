package database

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DefaultBotRelayScanIntervalMinutes 是 Relay 未配置时使用的扫描间隔。
const DefaultBotRelayScanIntervalMinutes = 5

// BotRelayHistoryLimit 是每条 Relay 路由保留的最近处理记录数。
const BotRelayHistoryLimit = 20

// BotRelay describes one channel deep-link relay owned by a SaveAny user.
type BotRelay struct {
	gorm.Model
	UserID         uint  `gorm:"not null"`
	User           User  `gorm:"constraint:OnDelete:CASCADE;"`
	SourceChatID   int64 `gorm:"uniqueIndex:idx_bot_relay_route"`
	SourceChat     string
	TargetBotID    int64 `gorm:"uniqueIndex:idx_bot_relay_route"`
	TargetBot      string
	Enabled        bool
	TimeoutSeconds int
	QuietSeconds   int
	// ScanIntervalMinutes 是来源频道的定时扫描间隔。
	ScanIntervalMinutes int `gorm:"not null;default:5"`
	// LastMessageID 是该路由已按顺序检查完成的最后一条频道消息。
	LastMessageID *int
	History       []BotRelayHistory `gorm:"constraint:OnDelete:CASCADE;"`
}

// BotRelayHistory 记录一条来源消息最近一次 Relay 处理结果。
type BotRelayHistory struct {
	gorm.Model
	BotRelayID uint `gorm:"not null;uniqueIndex:idx_bot_relay_history_message"`
	MessageID  int  `gorm:"not null;uniqueIndex:idx_bot_relay_history_message"`
	Success    bool
	Error      string
}

func CreateBotRelay(ctx context.Context, relay *BotRelay) error {
	return db.WithContext(ctx).Create(relay).Error
}

func GetAllBotRelays(ctx context.Context) ([]BotRelay, error) {
	var relays []BotRelay
	err := db.WithContext(ctx).Order("id").Find(&relays).Error
	return relays, err
}

func GetAllBotRelaysWithHistory(ctx context.Context) ([]BotRelay, error) {
	var relays []BotRelay
	err := db.WithContext(ctx).
		Preload("History", func(tx *gorm.DB) *gorm.DB { return tx.Order("updated_at DESC, id DESC") }).
		Order("id").Find(&relays).Error
	return relays, err
}

func GetBotRelayByID(ctx context.Context, id uint) (*BotRelay, error) {
	var relay BotRelay
	err := db.WithContext(ctx).First(&relay, id).Error
	return &relay, err
}

func UpdateBotRelay(ctx context.Context, relay *BotRelay, resetCursor bool) error {
	fields := []string{"user_id", "source_chat_id", "source_chat", "target_bot_id", "target_bot", "enabled", "timeout_seconds", "quiet_seconds", "scan_interval_minutes"}
	if resetCursor {
		fields = append(fields, "last_message_id")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&BotRelay{}).Where("id = ?", relay.ID).Select(fields).Updates(relay).Error; err != nil {
			return err
		}
		if resetCursor {
			return tx.Unscoped().Where("bot_relay_id = ?", relay.ID).Delete(&BotRelayHistory{}).Error
		}
		return nil
	})
}

// UpdateBotRelayLastMessageID 仅在路由未被修改时推进扫描游标。
func UpdateBotRelayLastMessageID(ctx context.Context, relay BotRelay, messageID int) error {
	result := db.WithContext(ctx).Model(&BotRelay{}).
		Where("id = ? AND user_id = ? AND source_chat_id = ? AND target_bot_id = ?", relay.ID, relay.UserID, relay.SourceChatID, relay.TargetBotID).
		UpdateColumn("last_message_id", messageID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("bot relay route changed while scanning")
	}
	return nil
}

// RecordBotRelayHistory 保存消息最近一次处理结果，并清理超出上限的旧记录。
func RecordBotRelayHistory(ctx context.Context, relay BotRelay, messageID int, processErr error) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var routeCount int64
		if err := tx.Model(&BotRelay{}).
			Where("id = ? AND user_id = ? AND source_chat_id = ? AND target_bot_id = ?", relay.ID, relay.UserID, relay.SourceChatID, relay.TargetBotID).
			Count(&routeCount).Error; err != nil {
			return err
		}
		if routeCount == 0 {
			return errors.New("bot relay route changed while scanning")
		}
		history := BotRelayHistory{BotRelayID: relay.ID, MessageID: messageID, Success: processErr == nil}
		if processErr != nil {
			history.Error = processErr.Error()
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "bot_relay_id"}, {Name: "message_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"success", "error", "updated_at"}),
		}).Create(&history).Error; err != nil {
			return err
		}
		var staleIDs []uint
		if err := tx.Model(&BotRelayHistory{}).
			Where("bot_relay_id = ?", relay.ID).
			Order("updated_at DESC, id DESC").
			Offset(BotRelayHistoryLimit).
			Pluck("id", &staleIDs).Error; err != nil {
			return err
		}
		if len(staleIDs) == 0 {
			return nil
		}
		return tx.Unscoped().Delete(&BotRelayHistory{}, staleIDs).Error
	})
}

func DeleteBotRelay(ctx context.Context, relay *BotRelay) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("bot_relay_id = ?", relay.ID).Delete(&BotRelayHistory{}).Error; err != nil {
			return err
		}
		return tx.Unscoped().Delete(relay).Error
	})
}
