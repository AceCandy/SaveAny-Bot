package core

import (
	"context"
	"errors"
	"testing"

	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
)

type trackedTask struct{}

func (trackedTask) Type() tasktype.TaskType       { return tasktype.TaskTypeTgfiles }
func (trackedTask) Title() string                 { return "Relay file" }
func (trackedTask) TaskID() string                { return "relay-task" }
func (trackedTask) Execute(context.Context) error { return nil }

func TestAddTaskEmitsQueuedEvent(t *testing.T) {
	events := make(chan taskevent.Event, 1)
	taskevent.SetGlobalSink(taskevent.SinkFunc(func(event taskevent.Event) { events <- event }))
	t.Cleanup(func() { taskevent.SetGlobalSink(nil) })

	ctx := taskevent.WithSource(t.Context(), taskevent.SourceRelay)
	if err := AddTask(ctx, trackedTask{}); err != nil {
		t.Fatalf("add task: %v", err)
	}
	event := <-events
	if event.Phase != taskevent.PhaseQueued || event.TaskID != "relay-task" || event.Source != taskevent.SourceRelay {
		t.Fatalf("unexpected queued event: %+v", event)
	}
	if err := CancelTask(t.Context(), "relay-task"); err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	event = <-events
	if event.Phase != taskevent.PhaseDone || !errors.Is(event.Err, context.Canceled) {
		t.Fatalf("unexpected cancelled event: %+v", event)
	}
}
