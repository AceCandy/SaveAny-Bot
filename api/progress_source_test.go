package api

import (
	"context"
	"testing"

	"github.com/krau/SaveAny-Bot/pkg/taskevent"
)

func TestProgressStoreTracksRelayTask(t *testing.T) {
	const taskID = "relay-progress-task"
	t.Cleanup(func() { DeleteTask(taskID) })

	store.Emit(taskevent.Event{
		TaskID:   taskID,
		TaskType: "tgfiles",
		Title:    "Relay file",
		Source:   taskevent.SourceRelay,
		Phase:    taskevent.PhaseStart,
	})
	info, ok := GetTask(taskID)
	if !ok || info.Source != taskevent.SourceRelay || info.Status != TaskStatusRunning {
		t.Fatalf("unexpected relay task: %+v, found=%v", info, ok)
	}
	if response := convertTaskProgressToResponse(info); response.Source != "relay" {
		t.Fatalf("task response source = %q", response.Source)
	}
	store.Emit(taskevent.Event{TaskID: taskID, Phase: taskevent.PhaseQueued})
	if info.Status != TaskStatusRunning {
		t.Fatalf("late queued event changed status to %s", info.Status)
	}

	store.Emit(taskevent.Event{TaskID: taskID, Phase: taskevent.PhaseDone, Err: context.Canceled})
	if info.Status != TaskStatusCancelled {
		t.Fatalf("cancelled task status = %s", info.Status)
	}
}
