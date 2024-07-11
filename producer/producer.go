// Package producer provides interfaces and types for IP address change detection and notification.
// It includes various kinds of producers that monitor IP address changes and notify subscribers
// through a unified interface.
package producer

import (
	"context"
	"log/slog"
	"net/netip"
)

// Source represents an observable, ever-changing state of IPv4 and/or IPv6 addresses.
type Source interface {
	// Snapshot returns a snapshot of the current IP addresses.
	Snapshot(ctx context.Context) (Message, error)
}

// Producer extends [Source] to include subscription and lifecycle management for
// IP address change notifications. It allows clients to subscribe for updates and
// controls the monitoring process, including starting and stopping the observation.
type Producer interface {
	Source

	// Subscribe returns a channel for receiving updates on IP address changes.
	// On subscription, an initial message with the current IP addresses is sent.
	// The channel is closed when the producer stops monitoring.
	Subscribe() <-chan Message

	// Run initiates the monitoring process for IP address changes. It blocks until
	// the provided context is canceled or an unrecoverable error occurs.
	Run(ctx context.Context, logger *slog.Logger) error
}

// Message is a snapshot of a source's current IPv4 and/or IPv6 addresses.
type Message struct {
	// IPv4 is the new IPv4 address, if any.
	// The address MUST NOT be an IPv4-mapped IPv6 address.
	IPv4 netip.Addr

	// IPv6 is the new IPv6 address, if any.
	IPv6 netip.Addr
}
