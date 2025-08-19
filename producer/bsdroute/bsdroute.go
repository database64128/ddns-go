// Package bsdroute implements a producer that utilizes routing information
// to obtain network interface IP addresses on supported BSD variants.
//
// The package supports any version of Darwin, any version of DragonFly BSD,
// FreeBSD 7 and above, NetBSD 6 and above, and OpenBSD 5.6 and above.
package bsdroute

import (
	"context"
	"errors"

	producerpkg "github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/tslog"
)

// PlatformUnsupportedError is returned when the platform is not supported by bsdroute.
type PlatformUnsupportedError struct{}

func (PlatformUnsupportedError) Error() string {
	return "bsdroute is only supported on Darwin, DragonFly BSD, FreeBSD, NetBSD, and OpenBSD"
}

func (PlatformUnsupportedError) Is(target error) bool {
	return target == errors.ErrUnsupported
}

var ErrPlatformUnsupported = PlatformUnsupportedError{}

// Source obtains the first IPv4 and IPv6 addresses from a network interface,
// using PF_ROUTE sysctl(2) on supported BSD variants. It only picks the first
// address of each family.
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

// ProducerConfig contains configuration options for the bsdroute producer.
type ProducerConfig struct {
	// Interface is the name of the network interface to monitor.
	Interface string `json:"interface"`
}

// NewProducer creates a new producer that monitors the IP addresses of a network interface.
func (cfg *ProducerConfig) NewProducer(logger *tslog.Logger) (*Producer, error) {
	return cfg.newProducer(logger)
}

// Producer monitors the IP addresses of a network interface using the routing socket,
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
func (p *Producer) Run(ctx context.Context) {
	p.run(ctx)
}
