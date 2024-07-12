// Package win32iphlp implements a producer that utilizes the Windows IP Helper API
// to obtain and monitor network interface IP addresses on Windows.
package win32iphlp

import (
	"context"
	"errors"
	"log/slog"

	producerpkg "github.com/database64128/ddns-go/producer"
)

// PlatformUnsupportedError is returned when the platform is not supported by win32iphlp.
type PlatformUnsupportedError struct{}

func (PlatformUnsupportedError) Error() string {
	return "win32iphlp is only supported on Windows"
}

func (PlatformUnsupportedError) Is(target error) bool {
	return target == errors.ErrUnsupported
}

var ErrPlatformUnsupported = PlatformUnsupportedError{}

// Source obtains the first IPv4 and IPv6 addresses from a network interface,
// using the Windows IP Helper API. It only picks the first address of each family.
//
// Source is not safe for concurrent use. Calls to Snapshot must be serialized.
//
// Source implements [producerpkg.Source].
type Source struct {
	source
}

// NewSource creates a new [Source].
func NewSource(name string) (*Source, error) {
	return newSource(name)
}

var _ producerpkg.Source = (*Source)(nil)

// Snapshot returns the first IPv4 and IPv6 addresses of the network interface.
//
// Snapshot implements [producerpkg.Source.Snapshot].
func (s *Source) Snapshot(_ context.Context) (producerpkg.Message, error) {
	return s.snapshot()
}

// ProducerConfig contains configuration options for the win32iphlp producer.
type ProducerConfig struct {
	// Interface is the name of the network interface to monitor.
	Interface string `json:"interface"`
}

// NewProducer creates a new producer that monitors the IP addresses of a network interface.
func (cfg *ProducerConfig) NewProducer() (*Producer, error) {
	return cfg.newProducer()
}

// Producer monitors the IP addresses of a network interface using the Windows IP Helper API,
// and broadcasts IP address change notifications to subscribers.
//
// Producer implements [producerpkg.Producer].
type Producer struct {
	producer
}

var _ producerpkg.Producer = (*Producer)(nil)

// Subscribe returns a channel for receiving updates on IP address changes.
//
// Subscribe implements [producerpkg.Producer.Subscribe].
func (p *Producer) Subscribe() <-chan producerpkg.Message {
	return p.subscribe()
}

// Run initiates the monitoring process for IP address changes.
//
// Run implements [producerpkg.Producer.Run].
func (p *Producer) Run(ctx context.Context, logger *slog.Logger) error {
	return p.run(ctx, logger)
}
