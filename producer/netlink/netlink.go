package netlink

import (
	"context"
	"errors"

	producerpkg "github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/tslog"
)

// PlatformUnsupportedError is returned when the platform is not supported by netlink.
type PlatformUnsupportedError struct{}

func (PlatformUnsupportedError) Error() string {
	return "netlink is only supported on Linux"
}

func (PlatformUnsupportedError) Is(target error) bool {
	return target == errors.ErrUnsupported
}

var ErrPlatformUnsupported = PlatformUnsupportedError{}

// ProducerConfig contains configuration options for the netlink producer.
type ProducerConfig struct {
	// Interface is the name of the network interface to monitor.
	Interface string `json:"interface"`

	// FromAddrLookupMain controls whether to add policy routing rules to let
	// packets originating from the interface addresses use the main routing table.
	//
	// This option is useful when a VPN connection is the default route, and the
	// physical interface still needs to handle incoming connections.
	FromAddrLookupMain bool `json:"from_addr_lookup_main,omitzero"`
}

// NewProducer creates a new producer that monitors the IP addresses of a network interface.
func (cfg *ProducerConfig) NewProducer(logger *tslog.Logger) (*Producer, error) {
	return cfg.newProducer(logger)
}

// Producer monitors the IP addresses of a network interface using Linux's netlink interface,
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
