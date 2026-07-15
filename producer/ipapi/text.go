package ipapi

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sync"

	"github.com/database64128/ddns-go/internal/httpreq"
	"github.com/database64128/ddns-go/producer"
)

// TextIPv4Source obtains the public IPv4 address from a text-based IP address API.
//
// TextIPv4Source implements [producer.Source].
type TextIPv4Source struct {
	textSource
}

// NewTextIPv4Source creates a new [TextIPv4Source].
//
//   - If client is nil, an internal IPv4-only HTTP client is used.
//   - If url is empty, it defaults to "https://api.ipify.org/".
//   - If userAgent is empty, it defaults to [httpreq.DefaultUserAgent].
func NewTextIPv4Source(client *http.Client, url, userAgent string) *TextIPv4Source {
	if client == nil {
		client = defaultHttpClient4()
	}
	if url == "" {
		url = "https://api.ipify.org/"
	}
	if userAgent == "" {
		userAgent = httpreq.DefaultUserAgent
	}
	return &TextIPv4Source{
		textSource: textSource{
			client:    client,
			url:       url,
			userAgent: userAgent,
		},
	}
}

var _ producer.Source = (*TextIPv4Source)(nil)

// Snapshot returns the current public IPv4 address.
//
// Snapshot implements [producer.Source.Snapshot].
func (s *TextIPv4Source) Snapshot(ctx context.Context) (producer.Message, error) {
	addr, err := s.get(ctx)
	if err != nil {
		return producer.Message{}, fmt.Errorf("failed to get IP address: %w", err)
	}
	if !addr.Is4() {
		return producer.Message{}, fmt.Errorf("not an IPv4 address: %s", addr)
	}
	return producer.Message{IPv4: addr}, nil
}

// TextIPv6Source obtains the public IPv6 address from a text-based IP address API.
//
// TextIPv6Source implements [producer.Source].
type TextIPv6Source struct {
	textSource
}

// NewTextIPv6Source creates a new [TextIPv6Source].
//
//   - If client is nil, an internal IPv6-only HTTP client is used.
//   - If url is empty, it defaults to "https://api6.ipify.org/".
//   - If userAgent is empty, it defaults to [httpreq.DefaultUserAgent].
func NewTextIPv6Source(client *http.Client, url, userAgent string) *TextIPv6Source {
	if client == nil {
		client = defaultHttpClient6()
	}
	if url == "" {
		url = "https://api6.ipify.org/"
	}
	if userAgent == "" {
		userAgent = httpreq.DefaultUserAgent
	}
	return &TextIPv6Source{
		textSource: textSource{
			client:    client,
			url:       url,
			userAgent: userAgent,
		},
	}
}

var _ producer.Source = (*TextIPv6Source)(nil)

// Snapshot returns the current public IPv6 address.
//
// Snapshot implements [producer.Source.Snapshot].
func (s *TextIPv6Source) Snapshot(ctx context.Context) (producer.Message, error) {
	addr, err := s.get(ctx)
	if err != nil {
		return producer.Message{}, fmt.Errorf("failed to get IP address: %w", err)
	}
	if !addr.Is6() {
		return producer.Message{}, fmt.Errorf("not an IPv6 address: %s", addr)
	}
	return producer.Message{IPv6: addr}, nil
}

// textSource obtains the public IP address from a text-based IP address API.
type textSource struct {
	client    *http.Client
	url       string
	userAgent string
}

// get retrieves the public IP address from the API.
func (s *textSource) get(ctx context.Context) (netip.Addr, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header["User-Agent"] = []string{s.userAgent}

	resp, err := s.client.Do(req)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	const maxResponseBodySize = int64(len("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff") + 25) // 64
	var buf bytes.Buffer
	if err = httpreq.ReadResponseBody(&buf, resp, maxResponseBodySize); err != nil {
		return netip.Addr{}, fmt.Errorf("failed to read response body: %w", err)
	}
	bodyBytes := buf.Bytes()

	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("unexpected status code %d: %q", resp.StatusCode, bodyBytes)
	}

	bodyBytes = bytes.TrimSpace(bodyBytes)

	addr, err := netip.ParseAddr(string(bodyBytes))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("failed to parse IP address: %w", err)
	}
	return addr.Unmap(), nil
}

// defaultHttpClient4 returns an [*http.Client] that behaves like
// [http.DefaultClient] but forces connections to use IPv4 only.
var defaultHttpClient4 = sync.OnceValue(func() *http.Client {
	transport := httpreq.DefaultHttpTransportClone()
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		switch network {
		case "tcp":
			network = "tcp4"
		case "udp":
			network = "udp4"
		}
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, addr)
	}
	return &http.Client{Transport: transport}
})

// defaultHttpClient6 returns an [*http.Client] that behaves like
// [http.DefaultClient] but forces connections to use IPv6 only.
var defaultHttpClient6 = sync.OnceValue(func() *http.Client {
	transport := httpreq.DefaultHttpTransportClone()
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		switch network {
		case "tcp":
			network = "tcp6"
		case "udp":
			network = "udp6"
		}
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, addr)
	}
	return &http.Client{Transport: transport}
})
