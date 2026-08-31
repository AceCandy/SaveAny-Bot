package database

import (
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func TestUpdateBotRelayPreservesOrResetsCursor(t *testing.T) {
	testDB, err := gorm.Open(GetDialect(filepath.Join(t.TempDir(), "relay.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	previousDB := db
	db = testDB
	t.Cleanup(func() { db = previousDB })
	if err := db.AutoMigrate(&User{}, &BotRelay{}, &BotRelayHistory{}); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	user := User{ChatID: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	cursor := 7
	relay := BotRelay{UserID: user.ID, SourceChatID: -1001, TargetBotID: 2, ScanIntervalMinutes: 5, LastMessageID: &cursor}
	if err := CreateBotRelay(t.Context(), &relay); err != nil {
		t.Fatalf("create relay: %v", err)
	}

	if err := UpdateBotRelayLastMessageID(t.Context(), relay, 8); err != nil {
		t.Fatalf("advance cursor: %v", err)
	}
	relay.TimeoutSeconds = 30
	if err := UpdateBotRelay(t.Context(), &relay, false); err != nil {
		t.Fatalf("update relay: %v", err)
	}
	got, err := GetBotRelayByID(t.Context(), relay.ID)
	if err != nil || got.LastMessageID == nil || *got.LastMessageID != 8 {
		t.Fatalf("preserved cursor = %v, error = %v; want 8", got.LastMessageID, err)
	}

	got.SourceChatID = -1002
	got.LastMessageID = nil
	if err := RecordBotRelayHistory(t.Context(), relay, 8, nil); err != nil {
		t.Fatalf("record relay history: %v", err)
	}
	if err := UpdateBotRelay(t.Context(), got, true); err != nil {
		t.Fatalf("reset relay: %v", err)
	}
	if err := RecordBotRelayHistory(t.Context(), relay, 9, nil); err == nil {
		t.Fatal("stale route history write succeeded; want route change error")
	}
	relays, err := GetAllBotRelaysWithHistory(t.Context())
	if err != nil || len(relays) != 1 {
		t.Fatalf("list relays = %d, error = %v; want one relay", len(relays), err)
	}
	got = &relays[0]
	if got.LastMessageID != nil {
		t.Fatalf("reset cursor = %v; want nil", got.LastMessageID)
	}
	if len(got.History) != 0 {
		t.Fatalf("history count = %d; want 0 after route change", len(got.History))
	}
}

func TestRecordBotRelayHistoryUpsertsAndKeepsRecentEntries(t *testing.T) {
	testDB, err := gorm.Open(GetDialect(filepath.Join(t.TempDir(), "relay-history.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	previousDB := db
	db = testDB
	t.Cleanup(func() { db = previousDB })
	if err := db.AutoMigrate(&User{}, &BotRelay{}, &BotRelayHistory{}); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	user := User{ChatID: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	relay := BotRelay{UserID: user.ID, SourceChatID: -1001, TargetBotID: 2, ScanIntervalMinutes: 5}
	if err := CreateBotRelay(t.Context(), &relay); err != nil {
		t.Fatalf("create relay: %v", err)
	}

	if err := RecordBotRelayHistory(t.Context(), relay, 1, errors.New("temporary failure")); err != nil {
		t.Fatalf("record failed history: %v", err)
	}
	if err := RecordBotRelayHistory(t.Context(), relay, 1, nil); err != nil {
		t.Fatalf("update successful history: %v", err)
	}
	relays, err := GetAllBotRelaysWithHistory(t.Context())
	if err != nil || len(relays) != 1 {
		t.Fatalf("list relays = %d, error = %v; want one relay", len(relays), err)
	}
	if len(relays[0].History) != 1 || !relays[0].History[0].Success || relays[0].History[0].Error != "" {
		t.Fatalf("updated history = %#v; want one successful entry", relays[0].History)
	}

	for messageID := 2; messageID <= BotRelayHistoryLimit+1; messageID++ {
		if err := RecordBotRelayHistory(t.Context(), relay, messageID, nil); err != nil {
			t.Fatalf("record message %d: %v", messageID, err)
		}
	}
	relays, err = GetAllBotRelaysWithHistory(t.Context())
	if err != nil {
		t.Fatalf("list relay history: %v", err)
	}
	history := relays[0].History
	if len(history) != BotRelayHistoryLimit || history[0].MessageID != BotRelayHistoryLimit+1 || history[len(history)-1].MessageID != 2 {
		t.Fatalf("history messages = %#v; want newest %d entries", history, BotRelayHistoryLimit)
	}
}
