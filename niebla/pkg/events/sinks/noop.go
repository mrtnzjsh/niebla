package sinks

import (
	"context"
	"niebla/pkg/events"
)

// NoopSink is a zero-allocation stub sink used when event publishing is disabled.
type NoopSink struct{}

// NewNoopSink returns a fresh NoopSink instance.
func NewNoopSink() *NoopSink {
	return &NoopSink{}
}

func (s *NoopSink) Name() string {
	return "noop"
}

func (s *NoopSink) Send(ctx context.Context, evt events.Event) error {
	return nil
}

func (s *NoopSink) Close() error {
	return nil
}
