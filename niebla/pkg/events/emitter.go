package events

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Emitter is the primary interface for publishing events from application components.
// All implementations must guarantee that Emit is non-blocking to protect latency-critical paths.
type Emitter interface {
	// Emit queues or dispatches an event. It returns immediately and never blocks.
	Emit(ctx context.Context, evt Event)

	// Close drains any pending queued events and releases underlying sink resources.
	Close() error
}

// -----------------------------------------------------------------------------
// NoopEmitter
// -----------------------------------------------------------------------------

// NoopEmitter is a zero-allocation emitter used when event publishing is disabled.
type NoopEmitter struct{}

// NewNoopEmitter returns a singleton-style NoopEmitter.
func NewNoopEmitter() *NoopEmitter {
	return &NoopEmitter{}
}

func (n *NoopEmitter) Emit(ctx context.Context, evt Event) {}

func (n *NoopEmitter) Close() error {
	return nil
}

// -----------------------------------------------------------------------------
// RecordingEmitter (useful for unit testing and assertion)
// -----------------------------------------------------------------------------

// RecordingEmitter captures emitted events in an in-memory thread-safe slice.
type RecordingEmitter struct {
	mu     sync.RWMutex
	events []Event
}

// NewRecordingEmitter returns an empty RecordingEmitter.
func NewRecordingEmitter() *RecordingEmitter {
	return &RecordingEmitter{
		events: make([]Event, 0),
	}
}

func (r *RecordingEmitter) Emit(ctx context.Context, evt Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, evt)
}

func (r *RecordingEmitter) Close() error {
	return nil
}

// Events returns a copy of all recorded events.
func (r *RecordingEmitter) Events() []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	copied := make([]Event, len(r.events))
	copy(copied, r.events)
	return copied
}

// Clear clears recorded events.
func (r *RecordingEmitter) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = r.events[:0]
}

// -----------------------------------------------------------------------------
// AsyncEmitter
// -----------------------------------------------------------------------------

// AsyncOption configures options for AsyncEmitter.
type AsyncOption func(*AsyncEmitter)

// WithBufferSize sets the event channel capacity (default: 256).
func WithBufferSize(size int) AsyncOption {
	return func(e *AsyncEmitter) {
		if size > 0 {
			e.bufferSize = size
		}
	}
}

// WithFilters sets event type filters (e.g. ["niebla.cl.*"]).
func WithFilters(filters []string) AsyncOption {
	return func(e *AsyncEmitter) {
		e.filters = filters
	}
}

// WithErrorHandler registers a callback for failed sends or dropped events.
func WithErrorHandler(fn func(err error, evt Event)) AsyncOption {
	return func(e *AsyncEmitter) {
		e.errorHandler = fn
	}
}

// AsyncEmitter delivers events asynchronously to a Sink using a buffered queue and worker goroutine.
type AsyncEmitter struct {
	sink         Sink
	filters      []string
	bufferSize   int
	queue        chan Event
	wg           sync.WaitGroup
	closeOnce    sync.Once
	closed       atomic.Bool
	droppedCount atomic.Uint64
	errorHandler func(err error, evt Event)
}

// NewAsyncEmitter constructs a new AsyncEmitter with the given Sink and optional parameters.
func NewAsyncEmitter(sink Sink, opts ...AsyncOption) *AsyncEmitter {
	if sink == nil {
		sink = &noopSinkAdapter{}
	}

	emitter := &AsyncEmitter{
		sink:       sink,
		bufferSize: 256,
	}

	for _, opt := range opts {
		opt(emitter)
	}

	emitter.queue = make(chan Event, emitter.bufferSize)
	emitter.wg.Add(1)
	go emitter.worker()

	return emitter
}

func (e *AsyncEmitter) worker() {
	defer e.wg.Done()

	for evt := range e.queue {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := e.sink.Send(ctx, evt)
		cancel()

		if err != nil && e.errorHandler != nil {
			e.errorHandler(err, evt)
		}
	}
}

// Emit queues the event. If the queue is full, the event is dropped non-blockingly.
func (e *AsyncEmitter) Emit(ctx context.Context, evt Event) {
	if e.closed.Load() {
		return
	}

	if !MatchFilter(e.filters, evt.Type) {
		return
	}

	select {
	case e.queue <- evt:
	default:
		e.droppedCount.Add(1)
		if e.errorHandler != nil {
			e.errorHandler(fmt.Errorf("events: queue full, dropped event %s (%s)", evt.ID, evt.Type), evt)
		}
	}
}

// DroppedCount returns the total number of events dropped due to a full queue.
func (e *AsyncEmitter) DroppedCount() uint64 {
	return e.droppedCount.Load()
}

// Close terminates the worker goroutine, drains queued events, and closes the underlying sink.
func (e *AsyncEmitter) Close() error {
	var err error
	e.closeOnce.Do(func() {
		e.closed.Store(true)
		close(e.queue)
		e.wg.Wait()
		if e.sink != nil {
			err = e.sink.Close()
		}
	})
	return err
}

// -----------------------------------------------------------------------------
// MultiEmitter
// -----------------------------------------------------------------------------

// MultiEmitter broadcasts events to multiple Emitters.
type MultiEmitter []Emitter

func (m MultiEmitter) Emit(ctx context.Context, evt Event) {
	for _, em := range m {
		if em != nil {
			em.Emit(ctx, evt)
		}
	}
}

func (m MultiEmitter) Close() error {
	var firstErr error
	for _, em := range m {
		if em != nil {
			if err := em.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

type noopSinkAdapter struct{}

func (n *noopSinkAdapter) Name() string                              { return "noop" }
func (n *noopSinkAdapter) Send(ctx context.Context, evt Event) error { return nil }
func (n *noopSinkAdapter) Close() error                              { return nil }
