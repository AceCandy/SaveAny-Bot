package shortcut

import (
	"errors"
	"testing"

	"github.com/celestix/gotgproto/dispatcher"
)

func TestEndGroupsWithErrorPreservesBothErrors(t *testing.T) {
	wantErr := errors.New("failed")
	got := endGroupsWithError(wantErr)
	if !errors.Is(got, dispatcher.EndGroups) || !errors.Is(got, wantErr) {
		t.Fatalf("endGroupsWithError() = %v; want both dispatcher and cause", got)
	}
}
