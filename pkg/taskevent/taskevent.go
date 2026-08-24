// Package taskevent provides a decoupled, context-scoped event bus for task
// lifecycle progress. Producers (task implementations) emit events via Emit;
// consumers (e.g. the API progress store, the Telegram message editor) register
// as Sinks and are injected through context. This keeps the task layer free of
// any concrete progress-display dependency, so new task types gain progress
// reporting for free and new observers can be added without touching tasks.
package taskevent

import (
	"context"
	"sync"
)

// Phase marks a stage in a task's lifecycle.
type Phase int

const (
	PhaseQueued Phase = iota
	PhaseStart
	PhaseProgress
	PhaseDone
)

func (p Phase) String() string {
	switch p {
	case PhaseQueued:
		return "queued"
	case PhaseStart:
		return "start"
	case PhaseProgress:
		return "progress"
	case PhaseDone:
		return "done"
	default:
		return "unknown"
	}
}

// Source 标识任务的创建入口。
type Source string

const (
	SourceAPI   Source = "api"
	SourceBot   Source = "bot"
	SourceRelay Source = "relay"
)

// Event describes a single progress observation for a task. Byte fields are
// populated by byte-stream tasks; file-count fields by count-based tasks. A
// task may fill whichever subset it has; observers ignore zero values.
type Event struct {
	TaskID          string
	TaskType        string
	Title           string
	Source          Source
	Phase           Phase
	TotalBytes      int64
	DownloadedBytes int64
	TotalFiles      int
	DownloadedFiles int
	Err             error
}

// Sink receives task events. Implementations must be safe for concurrent use.
type Sink interface {
	Emit(Event)
}

// SinkFunc is a function adapter for Sink.
type SinkFunc func(Event)

func (f SinkFunc) Emit(e Event) { f(e) }

type sinkKey struct{}
type sourceKey struct{}

var globalSink struct {
	sync.RWMutex
	Sink
}

// SetGlobalSink 设置接收全部任务事件的观察者。
func SetGlobalSink(sink Sink) {
	globalSink.Lock()
	globalSink.Sink = sink
	globalSink.Unlock()
}

// WithSource 返回携带任务来源的上下文。
func WithSource(ctx context.Context, source Source) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}

// SourceFromContext 读取上下文中的任务来源。
func SourceFromContext(ctx context.Context) Source {
	if ctx == nil {
		return ""
	}
	source, _ := ctx.Value(sourceKey{}).(Source)
	return source
}

// WithSink returns a ctx carrying the given sinks. Multiple sinks can be passed
// and all will receive every emitted event. Sinks already present in ctx are
// preserved.
func WithSink(ctx context.Context, sinks ...Sink) context.Context {
	if len(sinks) == 0 {
		return ctx
	}
	var existing []Sink
	if v, ok := ctx.Value(sinkKey{}).([]Sink); ok {
		existing = v
	}
	merged := make([]Sink, 0, len(existing)+len(sinks))
	merged = append(merged, existing...)
	merged = append(merged, sinks...)
	return context.WithValue(ctx, sinkKey{}, merged)
}

// Emit broadcasts an event to all sinks carried by ctx. It is a no-op when no
// sink is attached, so producers can call it unconditionally.
func Emit(ctx context.Context, e Event) {
	if ctx != nil {
		if sinks, ok := ctx.Value(sinkKey{}).([]Sink); ok {
			for _, sink := range sinks {
				sink.Emit(e)
			}
		}
	}
	globalSink.RLock()
	sink := globalSink.Sink
	globalSink.RUnlock()
	if sink != nil {
		sink.Emit(e)
	}
}
