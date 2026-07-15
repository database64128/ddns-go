package unifiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/database64128/ddns-go/internal/httpreq"
)

// Client is the Ubiquiti UniFi API client.
type Client struct {
	client   *http.Client
	sitesURL string
	apiKey   string
}

// NewClient returns a new Ubiquiti UniFi API client.
//
//   - If client is nil, [http.DefaultClient] is used.
//   - If baseURL is empty, it defaults to "https://unifi.local".
func NewClient(client *http.Client, baseURL, apiKey string) (*Client, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = "https://unifi.local"
	}

	sitesURL, err := url.JoinPath(baseURL, "/proxy/network/integration/v1/sites")
	if err != nil {
		return nil, fmt.Errorf("failed to join sites URL: %w", err)
	}

	return &Client{
		client:   client,
		sitesURL: sitesURL,
		apiKey:   apiKey,
	}, nil
}

// GetDeviceIPAddress returns the IP address of the specified device in the specified site.
func (c *Client) GetDeviceIPAddress(ctx context.Context, siteID, deviceID string) (netip.Addr, error) {
	deviceURL, err := url.JoinPath(c.sitesURL, siteID, "devices", deviceID)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("failed to join device URL: %w", err)
	}

	var device struct {
		IPAddress netip.Addr `json:"ipAddress"`
	}

	if err := clientDo(c.client, c.apiKey, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, deviceURL, nil)
	}, &device); err != nil {
		return netip.Addr{}, fmt.Errorf("failed to get device info: %w", err)
	}

	return device.IPAddress, nil
}

func clientDo(client *http.Client, apiKey string, newRequest func() (*http.Request, error), v any) error {
	req, err := newRequest()
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header["X-API-KEY"] = []string{apiKey}
	req.Header["Accept"] = []string{"application/json"}
	req.Header["User-Agent"] = []string{httpreq.DefaultUserAgent}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	const maxResponseBodySize = 128 * 1024 * 1024 // 128 MiB
	var buf bytes.Buffer
	if err := httpreq.ReadResponseBody(&buf, resp, maxResponseBodySize); err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	bodyBytes := buf.Bytes()

	if resp.StatusCode != http.StatusOK {
		var apiErr Error
		if err := json.Unmarshal(bodyBytes, &apiErr); err != nil {
			return fmt.Errorf("failed to unmarshal API error response: %w", err)
		}
		return &apiErr
	}

	if err := json.Unmarshal(bodyBytes, v); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return nil
}

// Error is the standard API error response.
type Error struct {
	StatusCode  int       `json:"statusCode"`
	StatusName  string    `json:"statusName"`
	Code        string    `json:"code"`
	Message     string    `json:"message"`
	Timestamp   time.Time `json:"timestamp"`
	RequestPath string    `json:"requestPath"`
	RequestID   string    `json:"requestId"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("status: %d %s, code: %s, message: %s, timestamp: %s, requestPath: %s, requestId: %s",
		e.StatusCode, e.StatusName, e.Code, e.Message, e.Timestamp.Format(time.RFC3339), e.RequestPath, e.RequestID)
}
