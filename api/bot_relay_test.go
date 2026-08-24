package api

import "testing"

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
