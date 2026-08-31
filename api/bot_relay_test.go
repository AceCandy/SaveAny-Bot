package api

import (
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/database"
)

func TestParseRelaySource(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantRef string
		wantID  int64
		wantErr bool
	}{
		{name: "username", input: " @mychannel ", wantRef: "mychannel"},
		{name: "channel ID", input: "-1002054107535", wantRef: "-1002054107535", wantID: -1002054107535},
		{name: "invalid negative ID", input: "-123", wantErr: true},
		{name: "invalid ID", input: "-100invalid", wantErr: true},
		{name: "empty", input: "", wantRef: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRef, gotID, err := parseRelaySource(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseRelaySource() error = %v, wantErr %v", err, tt.wantErr)
			}
			if gotRef != tt.wantRef || gotID != tt.wantID {
				t.Fatalf("parseRelaySource() = %q, %d; want %q, %d", gotRef, gotID, tt.wantRef, tt.wantID)
			}
		})
	}
}

func TestBotRelayToResponseUsesDefaultScanInterval(t *testing.T) {
	processedAt := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	history := database.BotRelayHistory{
		MessageID: 14,
		Success:   false,
		Error:     "failed",
	}
	history.UpdatedAt = processedAt
	response := botRelayToResponse(database.BotRelay{History: []database.BotRelayHistory{history}})
	if response.ScanIntervalMinutes != defaultRelayScanIntervalMinutes {
		t.Fatalf("ScanIntervalMinutes = %d; want %d", response.ScanIntervalMinutes, defaultRelayScanIntervalMinutes)
	}
	if len(response.History) != 1 || response.History[0].MessageID != 14 || response.History[0].Success || response.History[0].Error != "failed" || !response.History[0].ProcessedAt.Equal(processedAt) {
		t.Fatalf("History = %#v; want mapped failure record", response.History)
	}
}
