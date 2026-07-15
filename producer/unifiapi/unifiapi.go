// Package unifiapi provides the Ubiquiti UniFi API client producer.
package unifiapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/database64128/ddns-go/internal/httpreq"
	"github.com/database64128/ddns-go/jsoncfg"
	"github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/producer/internal/poller"
	"github.com/database64128/ddns-go/tslog"
)

// Source obtains the IPv4 address of a device from the Ubiquiti UniFi API.
//
// Source implements [producer.Source].
type Source struct {
	client   *Client
	siteID   string
	deviceID string
}

// NewSource creates a new [Source].
//
//   - If client is nil, [http.DefaultClient] is used.
//   - If baseURL is empty, it defaults to "https://unifi.local".
func NewSource(client *http.Client, baseURL, apiKey, siteID, deviceID string) (*Source, error) {
	c, err := NewClient(client, baseURL, apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create UniFi API client: %w", err)
	}

	return &Source{
		client:   c,
		siteID:   siteID,
		deviceID: deviceID,
	}, nil
}

var _ producer.Source = (*Source)(nil)

// Snapshot returns the current IPv4 address of the device.
//
// Snapshot implements [producer.Source.Snapshot].
func (s *Source) Snapshot(ctx context.Context) (producer.Message, error) {
	addr, err := s.client.GetDeviceIPAddress(ctx, s.siteID, s.deviceID)
	if err != nil {
		return producer.Message{}, fmt.Errorf("failed to get device IP address: %w", err)
	}
	if !addr.Is4() {
		return producer.Message{}, fmt.Errorf("not an IPv4 address: %s", addr)
	}
	return producer.Message{IPv4: addr}, nil
}

// ProducerConfig contains configuration options for the Ubiquiti UniFi API producer.
type ProducerConfig struct {
	// BaseURL is the base URL of the UniFi API endpoints.
	//
	// If empty, it defaults to "https://unifi.local".
	BaseURL string `json:"base_url,omitzero"`

	// APIKey is the API key for authenticating API requests.
	APIKey string `json:"api_key"`

	// SiteID is the site ID.
	SiteID string `json:"site_id"`

	// DeviceID is the device ID.
	DeviceID string `json:"device_id"`

	// RootCAPaths is a list of paths to PEM-encoded root CA certificates for verifying TLS server certificates.
	//
	// To trust the self-signed certificate, download the certificate and specify its path here.
	//
	// If empty, the system root CAs are used.
	RootCAPaths []string `json:"root_ca_paths,omitzero"`

	// ServerName is the server name to use when initializing a TLS connection.
	//
	// If empty, it is inferred from BaseURL.
	ServerName string `json:"server_name,omitzero"`

	// PollInterval is the interval between polling the UniFi API for the device IP address.
	//
	// If not positive, it defaults to 5 minutes.
	PollInterval jsoncfg.Duration `json:"poll_interval,omitzero"`
}

// NewProducer creates a new [producer.Producer] that monitors the IPv4 address of a device from the Ubiquiti UniFi API.
func (cfg *ProducerConfig) NewProducer(client *http.Client, logger *tslog.Logger) (producer.Producer, error) {
	if client == nil && (len(cfg.RootCAPaths) > 0 || cfg.ServerName != "") {
		var rootCAs *x509.CertPool
		if len(cfg.RootCAPaths) > 0 {
			rootCAs = x509.NewCertPool()
			for _, path := range cfg.RootCAPaths {
				cert, err := os.ReadFile(path)
				if err != nil {
					return nil, fmt.Errorf("failed to read root CA file %q: %w", path, err)
				}
				if !rootCAs.AppendCertsFromPEM(cert) {
					return nil, fmt.Errorf("failed to append root CA from file %q", path)
				}
			}
		}
		transport := httpreq.DefaultHttpTransportClone()
		transport.TLSClientConfig = &tls.Config{
			RootCAs:    rootCAs,
			ServerName: cfg.ServerName,
		}
		client = &http.Client{
			Transport: transport,
		}
	}

	source, err := NewSource(client, cfg.BaseURL, cfg.APIKey, cfg.SiteID, cfg.DeviceID)
	if err != nil {
		return nil, fmt.Errorf("failed to create source: %w", err)
	}

	pollInterval := cfg.PollInterval.Value()
	if pollInterval <= 0 {
		pollInterval = 5 * time.Minute
	}

	return poller.New(pollInterval, source, logger), nil
}
