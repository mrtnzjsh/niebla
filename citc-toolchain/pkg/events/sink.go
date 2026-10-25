package events

import "context"

// Sink represents a destination transport driver for published events.
type Sink interface {
	// Name returns the identifier of this sink driver (e.g. "webhook", "exec", "noop").
	Name() string

	// Send transmits a single event to the destination.
	Send(ctx context.Context, evt Event) error

	// Close terminates any active connections or background resources.
	Close() error
}
