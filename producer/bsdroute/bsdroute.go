// Package bsdroute implements a producer that utilizes routing information
// to obtain network interface IP addresses on supported BSD variants.
//
// The package supports any version of Darwin, any version of DragonFly BSD,
// FreeBSD 7 and above, NetBSD 6 and above, and OpenBSD 5.6 and above.
package bsdroute

import (
	"context"
	"errors"

	"github.com/database64128/ddns-go/jsoncfg"
	"github.com/database64128/ddns-go/producer"
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
// using routing information on supported BSD variants. It only picks the first
// address of each family.
//
// Source implements [producer.Source].
type Source struct {
	source
}

// NewSource creates a new [Source].
func NewSource(name string) (*Source, error) {
	return newSource(name)
}

var _ producer.Source = (*Source)(nil)

// Snapshot returns the first IPv4 and IPv6 addresses of the network interface.
//
// Snapshot implements [producer.Source.Snapshot].
func (s *Source) Snapshot(_ context.Context) (producer.Message, error) {
	return s.snapshot()
}

// ProducerConfig contains configuration options for the bsdroute producer.
type ProducerConfig struct {
	// Interface is the name of the network interface to monitor.
	Interface string `json:"interface"`

	// PollInterval is the interval between polling routing information for interface addresses.
	// If not positive, it defaults to 90 seconds.
	PollInterval jsoncfg.Duration `json:"poll_interval,omitzero"`
}

// NewProducer creates a new [producer.Producer] that monitors the IP addresses of a network interface.
func (cfg *ProducerConfig) NewProducer(logger *tslog.Logger) (producer.Producer, error) {
	return cfg.newProducer(logger)
}
