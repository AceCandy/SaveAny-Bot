package database

import (
	"context"

	"gorm.io/gorm"
)

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
}

func CreateBotRelay(ctx context.Context, relay *BotRelay) error {
	return db.WithContext(ctx).Create(relay).Error
}

func GetAllBotRelays(ctx context.Context) ([]BotRelay, error) {
	var relays []BotRelay
	err := db.WithContext(ctx).Order("id").Find(&relays).Error
	return relays, err
}

func GetBotRelayByID(ctx context.Context, id uint) (*BotRelay, error) {
	var relay BotRelay
	err := db.WithContext(ctx).First(&relay, id).Error
	return &relay, err
}

func GetEnabledBotRelaysBySourceChatID(ctx context.Context, chatID int64) ([]BotRelay, error) {
	var relays []BotRelay
	err := db.WithContext(ctx).Where("source_chat_id = ? AND enabled = ?", chatID, true).Find(&relays).Error
	return relays, err
}

func UpdateBotRelay(ctx context.Context, relay *BotRelay) error {
	return db.WithContext(ctx).Save(relay).Error
}

func DeleteBotRelay(ctx context.Context, relay *BotRelay) error {
	return db.WithContext(ctx).Unscoped().Delete(relay).Error
}
