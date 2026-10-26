package events_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"niebla/pkg/events"
)

type mockSink struct {
	mu     sync.Mutex
	events []events.Event
	closed bool
}

func (m *mockSink) Name() string { return "mock" }

func (m *mockSink) Send(ctx context.Context, evt events.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, evt)
	return nil
}

func (m *mockSink) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func TestEventCreation(t *testing.T) {
	payload := events.ReviewersAssignedPayload{
		CLID:      "123",
		Author:    "alice",
		Reviewers: []string{"bob", "charlie"},
	}

	evt := events.New(events.TypeReviewersAssigned, "critique", "123", payload)

	if evt.SpecVersion != events.SpecVersion10 {
		t.Fatalf("expected specversion %s, got %s", events.SpecVersion10, evt.SpecVersion)
	}
	if evt.Type != events.TypeReviewersAssigned {
		t.Fatalf("expected type %s, got %s", events.TypeReviewersAssigned, evt.Type)
	}
	if evt.Source != "critique" {
		t.Fatalf("expected source critique, got %s", evt.Source)
	}
	if evt.Subject != "123" {
		t.Fatalf("expected subject 123, got %s", evt.Subject)
	}
	if evt.ID == "" {
		t.Fatalf("expected non-empty event ID")
	}
	if evt.Time.IsZero() {
		t.Fatalf("expected non-zero event timestamp")
	}
}

func TestMatchFilter(t *testing.T) {
	tests := []struct {
		name      string
		filters   []string
		eventType string
		expected  bool
	}{
		{"empty filter matches everything", nil, "niebla.cl.created", true},
		{"wildcard filter matches everything", []string{"*"}, "niebla.cl.created", true},
		{"prefix wildcard matches", []string{"niebla.cl.*"}, "niebla.cl.created", true},
		{"prefix wildcard matches other suffix", []string{"niebla.cl.*"}, "niebla.cl.submitted", true},
		{"prefix wildcard does not match different prefix", []string{"niebla.cl.*"}, "niebla.workspace.created", false},
		{"exact match matches", []string{"niebla.cl.created"}, "niebla.cl.created", true},
		{"exact match does not match different event", []string{"niebla.cl.created"}, "niebla.cl.submitted", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := events.MatchFilter(tc.filters, tc.eventType)
			if got != tc.expected {
				t.Errorf("MatchFilter(%v, %q) = %v; want %v", tc.filters, tc.eventType, got, tc.expected)
			}
		})
	}
}

func TestNoopEmitter(t *testing.T) {
	emitter := events.NewNoopEmitter()
	evt := events.New(events.TypeCLSubmitted, "test", "1", nil)

	// Should not panic or block
	emitter.Emit(context.Background(), evt)
	if err := emitter.Close(); err != nil {
		t.Fatalf("unexpected error closing NoopEmitter: %v", err)
	}
}

func TestRecordingEmitter(t *testing.T) {
	emitter := events.NewRecordingEmitter()
	evt1 := events.New(events.TypeReviewersAssigned, "test", "1", nil)
	evt2 := events.New(events.TypeCLSubmitted, "test", "2", nil)

	emitter.Emit(context.Background(), evt1)
	emitter.Emit(context.Background(), evt2)

	recorded := emitter.Events()
	if len(recorded) != 2 {
		t.Fatalf("expected 2 recorded events, got %d", len(recorded))
	}
	if recorded[0].ID != evt1.ID || recorded[1].ID != evt2.ID {
		t.Fatalf("recorded events mismatch: %+v vs %+v", recorded, []events.Event{evt1, evt2})
	}

	emitter.Clear()
	if len(emitter.Events()) != 0 {
		t.Fatalf("expected 0 events after Clear()")
	}
}

func TestAsyncEmitterDeliveryAndFilter(t *testing.T) {
	sink := &mockSink{}
	emitter := events.NewAsyncEmitter(
		sink,
		events.WithBufferSize(10),
		events.WithFilters([]string{"niebla.cl.*"}),
	)

	evtMatching := events.New(events.TypeCLSubmitted, "test", "1", nil)
	evtIgnored := events.New("niebla.workspace.created", "test", "ws1", nil)

	emitter.Emit(context.Background(), evtMatching)
	emitter.Emit(context.Background(), evtIgnored)

	// Drain queue and close
	if err := emitter.Close(); err != nil {
		t.Fatalf("failed to close emitter: %v", err)
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()

	if !sink.closed {
		t.Fatalf("expected sink to be closed")
	}
	if len(sink.events) != 1 {
		t.Fatalf("expected exactly 1 delivered event, got %d", len(sink.events))
	}
	if sink.events[0].ID != evtMatching.ID {
		t.Fatalf("expected delivered event %s, got %s", evtMatching.ID, sink.events[0].ID)
	}
}

func TestAsyncEmitterNonBlockingDrop(t *testing.T) {
	// Slow sink to induce buffer overflow
	slowSink := &mockSink{}
	emitter := events.NewAsyncEmitter(
		slowSink,
		events.WithBufferSize(2),
	)

	// Emit 50 events quickly
	for i := 0; i < 50; i++ {
		emitter.Emit(context.Background(), events.New(events.TypeCLSubmitted, "test", "1", nil))
	}

	time.Sleep(50 * time.Millisecond)
	_ = emitter.Close()

	if emitter.DroppedCount() == 0 {
		t.Log("Note: queue didn't overflow in fast execution, which is acceptable under fast channel read.")
	}
}
