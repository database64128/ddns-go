// Package ipapi implements the IP API producer.
package ipapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/database64128/ddns-go/jsoncfg"
	"github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/producer/internal/poller"
	"github.com/database64128/ddns-go/tslog"
)

// ProducerConfig contains configuration options for the IP API producer.
type ProducerConfig struct {
	// Source is the IP address API to use.
	//
	//   - "text-ipv4": Text-based IPv4 address API.
	//   - "text-ipv6": Text-based IPv6 address API.
	Source string `json:"source"`

	// URL is the URL of the IP address API.
	// If empty, the source default is used.
	URL string `json:"url,omitzero"`

	// PollInterval is the interval between polling the IP address API.
	// If not positive, it defaults to 5 minutes.
	PollInterval jsoncfg.Duration `json:"poll_interval,omitzero"`
}

// NewProducer creates a new [producer.Producer] that monitors the public IP address from an IP address API.
//
// If client is nil, [http.DefaultClient] is used.
func (cfg *ProducerConfig) NewProducer(client *http.Client, logger *tslog.Logger) (producer.Producer, error) {
	var source producer.Source
	switch cfg.Source {
	case "text-ipv4":
		source = NewTextIPv4Source(client, cfg.URL)
	case "text-ipv6":
		source = NewTextIPv6Source(client, cfg.URL)
	default:
		return nil, fmt.Errorf("unknown source: %q", cfg.Source)
	}

	pollInterval := cfg.PollInterval.Value()
	if pollInterval <= 0 {
		pollInterval = 5 * time.Minute
	}

	return poller.New(pollInterval, source, logger), nil
}
