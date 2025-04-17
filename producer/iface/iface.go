// Package iface implements the network interface producer.
package iface

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/database64128/ddns-go/jsoncfg"
	"github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/producer/internal/poller"
	"github.com/database64128/ddns-go/tslog"
)

// Source obtains the first IPv4 and IPv6 addresses from a network interface,
// using Go's net package. It only picks the first address of each family.
//
// Source implements [producer.Source].
type Source struct {
	name string
}

// NewSource creates a new [Source].
func NewSource(name string) *Source {
	return &Source{name: name}
}

var _ producer.Source = (*Source)(nil)

// Snapshot returns the first IPv4 and IPv6 addresses of the network interface.
//
// Snapshot implements [producer.Source.Snapshot].
func (s *Source) Snapshot(_ context.Context) (producer.Message, error) {
	iface, err := net.InterfaceByName(s.name)
	if err != nil {
		return producer.Message{}, fmt.Errorf("failed to get interface by name %q: %w", s.name, err)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return producer.Message{}, fmt.Errorf("failed to get addresses of interface %q: %w", s.name, err)
	}

	var msg producer.Message
	for _, addr := range addrs {
		ip, ok := netip.AddrFromSlice(addr.(*net.IPNet).IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if ip.IsLinkLocalUnicast() {
			continue
		}
		if ip.Is4() {
			if !msg.IPv4.IsValid() {
				msg.IPv4 = ip
			}
		} else {
			if !msg.IPv6.IsValid() {
				msg.IPv6 = ip
			}
		}
	}
	return msg, nil
}

// ProducerConfig contains configuration options for the network interface producer.
type ProducerConfig struct {
	// Interface is the name of the network interface to monitor.
	Interface string `json:"interface"`

	// PollInterval is the interval between polling the network interface.
	// If not positive, it defaults to 90 seconds.
	PollInterval jsoncfg.Duration `json:"poll_interval,omitzero"`
}

// NewProducer creates a new [producer.Producer] that monitors the first IPv4 and IPv6 addresses of a network interface.
func (cfg *ProducerConfig) NewProducer(logger *tslog.Logger) (producer.Producer, error) {
	if cfg.Interface == "" {
		return nil, errors.New("interface name is required")
	}

	source := NewSource(cfg.Interface)

	pollInterval := cfg.PollInterval.Value()
	if pollInterval <= 0 {
		pollInterval = 90 * time.Second
	}

	return poller.New(pollInterval, source, logger), nil
}
